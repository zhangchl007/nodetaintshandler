package startup

import "k8s.io/client-go/tools/cache"

// tiny helper to avoid importing full handler struct each place
func cacheResourceHandler(fn func(obj interface{})) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    fn,
		UpdateFunc: func(_, newObj interface{}) { fn(newObj) },
	}
}
