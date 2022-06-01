package main

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	v12 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"nginx-controller/pkg/apis/nginx_controller/v1alpha1"
	clientset "nginx-controller/pkg/generated/clientset/versioned"
	nginxscheme "nginx-controller/pkg/generated/clientset/versioned/scheme"
	informers "nginx-controller/pkg/generated/informers/externalversions/nginx_controller/v1alpha1"
	listers "nginx-controller/pkg/generated/listers/nginx_controller/v1alpha1"
	"strconv"
	"time"
)

const controllerAgentName = "nginx-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Foo fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Foo"
	// MessageResourceSynced is the message used for an Event fired when a Foo
	// is synced successfully
	MessageResourceSynced = "Nginx synced successfully"
)

// Controller is the controller implementation for Foo resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// nginxclientset is a clientset for our own API group
	nginxclientset clientset.Interface

	podLister   v1.PodLister
	podsSynced  cache.InformerSynced
	nginxLister listers.NginxLister
	nginxSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	nginxclientset clientset.Interface,
	podInformer v12.PodInformer,
	nginxInformer informers.NginxInformer) *Controller {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(nginxscheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:  kubeclientset,
		nginxclientset: nginxclientset,
		podLister:      podInformer.Lister(),
		podsSynced:     podInformer.Informer().HasSynced,
		nginxLister:    nginxInformer.Lister(),
		nginxSynced:    nginxInformer.Informer().HasSynced,
		workqueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Nginx"),
		recorder:       recorder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when Foo resources change
	nginxInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueNginx,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueNginx(new)
		},
		DeleteFunc: controller.enqueueNginx,
	})
	// Set up an event handler for when Deployment resources change. This
	// handler will lookup the owner of the given Deployment, and if it is
	// owned by a Foo resource then the handler will enqueue that Foo resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Deployment resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newPod := new.(*corev1.Pod)
			oldPod := old.(*corev1.Pod)
			if newPod.ResourceVersion == oldPod.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(workers int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Nginx controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.podsSynced, c.nginxSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process Foo resources
	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		if err := c.syncHandler(key); err != nil && !errors.IsNotFound(err) {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Foo resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) (err error) {
	var (
		running, pending, podId int
		nginxNotFound           bool
		created                 *corev1.Pod
	)

	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	if namespace == "" {
		namespace = "default"
	}

	// Get the Foo resource with this namespace/name
	nginx, err := c.nginxLister.Nginxes(namespace).Get(name)
	if err != nil && !errors.IsNotFound(err) {
		return
	}

	if errors.IsNotFound(err) {
		nginxNotFound = true
	}

	// 筛选出关联的POD
	selector := labels.NewSelector()
	var requirement *labels.Requirement
	if requirement, err = labels.NewRequirement("nginxKey", selection.Equals, []string{"my-nginx"}); err != nil {
		return
	}
	selector = selector.Add(*requirement) // 注意返回值覆盖

	// 出于调度实时性的需要, POD列表取apiserver最新的状态, 不走local cache
	podList, err := c.kubeclientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return err
	}

	pods := podList.Items
	podCount := len(pods)

	// nginx已删除, 清理所有关联PODS
	if nginxNotFound {
		for i := 0; i < len(pods); i++ {
			if err = c.kubeclientset.CoreV1().Pods(pods[i].Namespace).Delete(context.TODO(), pods[i].Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
				return
			}
			klog.Infoln("[Nginx - 清理POD]", key, pods[i].Name)
		}
		return
	}

	// Nginx部署策略: 确保足够数量的POD运行即可
	// 1, running+pending<replicas, 那么创建
	// 2, running+pending>replicas, 那么删除
	// 3, 其他状态的删除

	// 统计一下running和pending的POD个数
	for i := 0; i < podCount; i++ {
		if pods[i].Status.Phase == corev1.PodRunning {
			running++
		} else if pods[i].Status.Phase == corev1.PodPending {
			pending++
		} else { // 其他状态的删除
			if err = c.kubeclientset.CoreV1().Pods(pods[i].Namespace).Delete(context.TODO(), pods[i].Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
				return
			}
		}
	}

	if running+pending < nginx.Spec.Replicas {
		scale := nginx.Spec.Replicas - running - pending
		for i := 0; i < scale; i++ {
			pod := newPod(nginx)
			if namespace = nginx.Namespace; namespace == "" {
				namespace = "default"
			}
			if created, err = c.kubeclientset.CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{}); err != nil {
				return
			}
			pods = append(pods, *created)
			podId++
			podCount++
			klog.Infoln("[Nginx - 扩容POD]", key, created.Name)
		}
	} else {
		toDelete := running + pending - nginx.Spec.Replicas
		// 先删pending的
		for i := 0; i < len(pods); i++ {
			if toDelete == 0 {
				break
			}
			if pods[i].Status.Phase != corev1.PodPending {
				continue
			}
			if err = c.kubeclientset.CoreV1().Pods(pods[i].Namespace).Delete(context.TODO(), pods[i].Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
				return
			}
			toDelete--
			klog.Infoln("[Nginx - 缩容POD]", key, pods[i].Name)
		}
		// 再删running的
		for i := 0; i < len(pods); i++ {
			if toDelete == 0 {
				break
			}
			if pods[i].Status.Phase != corev1.PodRunning {
				continue
			}
			if err = c.kubeclientset.CoreV1().Pods(pods[i].Namespace).Delete(context.TODO(), pods[i].Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
				return
			}
			toDelete--
			klog.Infoln("[Nginx - 缩容POD]", key, pods[i].Name)
		}
	}
	klog.Infoln("[Nginx - 更新]", key, "running:", running, "pending:", pending, "total:", podCount)

	c.recorder.Event(nginx, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return err
}

// enqueueNginx takes a Foo resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Foo.
func (c *Controller) enqueueNginx(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the Foo resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that Foo resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var (
		pod      *corev1.Pod
		ok       bool
		nginxKey string
		hasLabel bool
	)

	// 反解出Pod
	if pod, ok = obj.(*corev1.Pod); !ok {
		return
	}

	// 确认属于nginx部署的POD
	if nginxKey, hasLabel = pod.Labels["nginxKey"]; !hasLabel {
		return // 不属于nginx部署的POD， 忽略
	}

	// 投递给nginx的workqueue
	c.workqueue.AddRateLimited(nginxKey)
	klog.Infoln("[POD - 更新]", nginxKey, pod.Name)
	return
}

// newPod creates a new Pod for a Nginx resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the Nginx resource that 'owns' it.
func newPod(nginx *v1alpha1.Nginx) *corev1.Pod {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "my-nginx-" + strconv.Itoa(int(time.Now().UnixNano())),
			Labels: map[string]string{"nginxKey": nginx.Name},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "nginx", Image: "nginx:latest"},
			},
		},
	}
	return &pod
}
