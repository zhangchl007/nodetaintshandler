package startup

import (
	"context"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

// Controller watches Nodes with the startup taint and removes it once the init pod on that node is Ready.
type Controller struct {
	client     kubernetes.Interface
	podIndexer cache.Indexer
}

func NewController(client kubernetes.Interface) *Controller {
	return &Controller{client: client}
}

func (c *Controller) Run(stop <-chan struct{}) {
	factory := informers.NewSharedInformerFactory(c.client, 30*time.Second)
	nodeInformer := factory.Core().V1().Nodes().Informer()
	podInformer := factory.Core().V1().Pods().Informer()

	// Index pods by node name for efficient lookup
	_ = podInformer.AddIndexers(cache.Indexers{
		"byNode": func(obj interface{}) ([]string, error) {
			p, ok := obj.(*corev1.Pod)
			if !ok || p.Spec.NodeName == "" {
				return []string{}, nil
			}
			return []string{p.Spec.NodeName}, nil
		},
	})

	c.podIndexer = podInformer.GetIndexer()

	nodeInformer.AddEventHandler(cacheResourceHandler(c.handleNode))
	podInformer.AddEventHandler(cacheResourceHandler(c.handlePod))
	factory.Start(stop)
	factory.WaitForCacheSync(stop)

	// Optional backfill: add taint to new nodes that missed webhook (disabled by default)
	if os.Getenv("STARTUP_BACKFILL") == "1" {
		c.backfillTaint()
	}

	<-stop
}

func (c *Controller) handlePod(obj interface{}) {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	if p.Labels[StartPodLabelKey] != StartPodLabelValue {
		return
	}
	if p.Spec.NodeName == "" {
		return
	}
	// Re-evaluate node when startup pod condition changes
	n, err := c.client.CoreV1().Nodes().Get(context.TODO(), p.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		return
	}
	if HasStartupTaint(n) {
		c.handleNode(n)
	}
}

func (c *Controller) handleNode(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}
	if !HasStartupTaint(node) {
		return
	}
	ready, err := c.startupPodReady(node.Name)
	if err != nil {
		klog.Warningf("check startup pod on node %s: %v", node.Name, err)
		return
	}
	if !ready {
		return
	}
	if err := c.removeStartupTaint(node); err != nil {
		klog.Warningf("remove startup taint from %s: %v", node.Name, err)
	} else {
		klog.Infof("Removed startup taint from node %s", node.Name)
	}
}

func HasStartupTaint(node *corev1.Node) bool {
	for _, t := range node.Spec.Taints {
		if t.Key == TaintKey && t.Value == TaintValue && t.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func (c *Controller) startupPodReady(nodeName string) (bool, error) {
	// Use index (fall back to API list if indexer nil)
	if c.podIndexer != nil {
		objs, _ := c.podIndexer.ByIndex("byNode", nodeName)
		for _, o := range objs {
			p := o.(*corev1.Pod)
			if p.Labels[StartPodLabelKey] != StartPodLabelValue {
				continue
			}
			if p.Annotations != nil && p.Annotations[StartPodReadyAnnotation] == "true" {
				return true, nil
			}
			allReady := true
			for _, cs := range p.Status.ContainerStatuses {
				if !cs.Ready {
					allReady = false
					break
				}
			}
			if !allReady {
				continue
			}
			for _, cond := range p.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
		}
		return false, nil
	}
	// Fallback to API list
	pods, err := c.client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", nodeName).String(),
		LabelSelector: StartPodLabelKey + "=" + StartPodLabelValue,
	})
	if err != nil {
		return false, err
	}
	for _, p := range pods.Items {
		if p.Spec.NodeName != nodeName {
			continue
		}
		if p.Annotations != nil && p.Annotations[StartPodReadyAnnotation] == "true" {
			return true, nil
		}
		allReady := true
		readyCond := false
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				allReady = false
				break
			}
		}
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				readyCond = true
				break
			}
		}
		if allReady && readyCond {
			return true, nil
		}
	}
	return false, nil
}

func (c *Controller) removeStartupTaint(node *corev1.Node) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, err := c.client.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		newTaints := n.Spec.Taints[:0]
		changed := false
		for _, t := range n.Spec.Taints {
			if t.Key == TaintKey && t.Value == TaintValue && t.Effect == corev1.TaintEffectNoSchedule {
				changed = true
				continue
			}
			newTaints = append(newTaints, t)
		}
		if !changed {
			return nil
		}
		n.Spec.Taints = newTaints
		if n.Annotations == nil {
			n.Annotations = map[string]string{}
		}
		n.Annotations[NodeStartupCompletedAnnotation] = strconv.FormatInt(time.Now().Unix(), 10)
		_, err = c.client.CoreV1().Nodes().Update(context.TODO(), n, metav1.UpdateOptions{})
		return err
	})
}

func (c *Controller) backfillTaint() {
	nodes, err := c.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Warningf("backfill list nodes: %v", err)
		return
	}
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if HasStartupTaint(n) {
			continue
		}
		// Skip nodes already marked completed
		if n.Annotations != nil && n.Annotations[NodeStartupCompletedAnnotation] != "" {
			continue
		}
		// Add taint only if no non-system pods running (avoid disrupting established workloads)
		if c.hasWorkloadPods(n.Name) {
			continue
		}
		n.Spec.Taints = append(n.Spec.Taints, StartupTaint)
		if _, err := c.client.CoreV1().Nodes().Update(context.TODO(), n, metav1.UpdateOptions{}); err != nil {
			klog.Warningf("backfill add taint %s: %v", n.Name, err)
		} else {
			klog.Infof("Backfilled startup taint on node %s", n.Name)
		}
	}
}

func (c *Controller) hasWorkloadPods(nodeName string) bool {
	if c.podIndexer != nil {
		objs, _ := c.podIndexer.ByIndex("byNode", nodeName)
		for _, o := range objs {
			p := o.(*corev1.Pod)
			ns := p.Namespace
			if ns != "kube-system" && ns != "kube-public" {
				return true
			}
		}
		return false
	}
	// Fallback to API list
	pods, err := c.client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", nodeName).String(),
	})
	if err != nil {
		return true
	}
	for _, p := range pods.Items {
		if p.Namespace != "kube-system" && p.Namespace != "kube-public" {
			return true
		}
	}
	return false
}
