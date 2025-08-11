package startup

import corev1 "k8s.io/api/core/v1"

const (
	TaintKey       = "startup.k8s.io/initializing"
	TaintValue     = "wait"
	TaintEffectStr = "NoSchedule"

	StartPodLabelKey   = "startup.k8s.io/component"
	StartPodLabelValue = "init"

	// Annotation a startup DaemonSet Pod can set when its logic is complete (optional shortcut)
	StartPodReadyAnnotation = "startup.k8s.io/ready"

	// Annotation the controller sets on the Node after taint removal (auditing)
	NodeStartupCompletedAnnotation = "startup.k8s.io/completedAt"
)

var StartupTaint = corev1.Taint{
	Key:    TaintKey,
	Value:  TaintValue,
	Effect: corev1.TaintEffectNoSchedule,
}
