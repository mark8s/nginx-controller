# Nginx Controller

一个监控nginx数量的controller

## Usage
启动controller
```shell
$ go build .
$ ./nginx-controller -kubeconfig /root/.kube/config -alsologtostderr true
```

apply crd
```shell
kubectl apply -f ./artifacts/example/crd.yaml
```

apply cr
```shell
kubectl apply -f ./artifacts/example/example-nginx.yaml
```

## Test
当cr中定义的副本数为2时，会自动启动2个nginx的pod
```shell
$ vim example-nginx.yaml 
$ kubectl apply -f example-nginx.yaml 
$ cat example-nginx.yaml 
apiVersion: demo.mark8s.io/v1alpha1
kind: Nginx
metadata:
  name: my-nginx
spec:
  replicas: 2
$ kubectl get po
NAME                            READY   STATUS    RESTARTS   AGE
nginx-pod-1654078536671008205   1/1     Running   0          14m
nginx-pod-1654079421678202242   1/1     Running   0          9s
```

当cr中定义的副本数为5时，会自动启动5个nginx的pod.效果类似 replicaSet
```shell
$ vim example-nginx.yaml 
$ kubectl apply -f example-nginx.yaml 
$ cat example-nginx.yaml 
apiVersion: demo.mark8s.io/v1alpha1
kind: Nginx
metadata:
  name: my-nginx
spec:
  replicas: 5
$ kubectl get po
NAME                            READY   STATUS    RESTARTS   AGE
nginx-pod-1654078536671008205   1/1     Running   0          17m
nginx-pod-1654079421678202242   1/1     Running   0          2m43s
nginx-pod-1654079527716529235   1/1     Running   0          57s
nginx-pod-1654079527722074237   1/1     Running   0          57s
nginx-pod-1654079527724902515   1/1     Running   0          57s
```

## Reference

[sample-controller](https://github.com/kubernetes/sample-controller)

[owenliang/k8s-client-go](https://github.com/owenliang/k8s-client-go/tree/master/demo10)

