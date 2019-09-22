# Kubernetes介绍
<a href="url"><img src="https://ss1.bdstatic.com/70cFuXSh_Q1YnxGkpoWK1HF6hhy/it/u=2232728375,914299056&fm=26&gp=0.jpg" align="left" height="122" width="200" ></a>

**Kubernetes（简称k8s）是Google在2014年6月开源的一个「容器集群管理系统」,用于「跨主机集群」的容器(如docker)自动部署、扩展以及运行应用程序容器的平台，目标是让部署容器化的应用简单并且高效,提供了资源调度
、部署管理、服务发现、扩容缩容、监控，维护等一整套功能。**  
1)隐藏资源管理和错误处理，用户仅需要关注应用的开发。  
2)服务高可用、高可靠。  
3)可将负载运行在由成千上万的机器联合而成的集群中。  





# Kubernetes安装
* 环境

名称| ip | 角色  | 操作系统 | 内核 | docker版本
:-: | :-: | :-: | :-: | :-:  | :-:
k8s-master-01 | 10.177.177.34 | master | centos 7.6 | 5.2.14-1.el7.elrepo.x86_64 | docker-ce-18.6.0
k8s-master-02  | 10.177.177.35 | master | centos 7.6 | 5.2.14-1.el7.elrepo.x86_64 | docker-ce-18.6.0
k8s-master-03 | 10.177.177.36 | master | centos 7.6 | 5.2.14-1.el7.elrepo.x86_64 | docker-ce-18.6.0
k8s-node-01 | 10.177.177.37 | node | centos 7.6 | 5.2.14-1.el7.elrepo.x86_64 | docker-ce-18.6.0
k8s-node-02 | 10.177.177.38 | node | centos 7.6 | 5.2.14-1.el7.elrepo.x86_64 | docker-ce-18.6.0
keepalive | 10.177.177.39 | vip

* 拉取项目
```shell
git clone https://dev.biosino.org/git/zhaoli/k8s.git
```
* 每个节点安装初始化环境
```shell
tar zxvf install/kube1.15.3.tar.gz && cp kube/bin/kubeadm /usr/bin/kubeadm && cd kube/shell && sh init.sh
```

* k8s-master-01
```shell
echo "10.177.177.34 apiserver.cluster.local" >> /etc/hosts
#初始化集群
kubeadm init --config=kubeadm-config.yaml --experimental-upload-certs
#创建使用命令权限
mkdir ~/.kube && cp /etc/kubernetes/admin.conf ~/.kube/config
# 安装calico
kubectl apply -f soft/calico/calico.yaml
```
> 执行完会输出一些日志，里面包含join需要用的命令

* k8s-master-02
```shell
echo "10.177.177.34 apiserver.cluster.local" >> /etc/hosts
#加入集群
kubeadm join apiserver.cluster.local:6443 --token 9xl3oj.xwpls6jft9tov81o \
    --discovery-token-ca-cert-hash sha256:3aac728342ab0e2993b0cf7abd13ae703eb032828e26121ce7536d58083ad66f \
    --control-plane --certificate-key 753b1037287af5711a9b12678983a6141da93ec5911f6ea868081b6caaff7ffd
#创建使用命令权限
mkdir ~/.kube && cp /etc/kubernetes/admin.conf ~/.kube/config
#将apiserver.cluster.local映射到本机ip
sed -i s/10.177.177.34 apiserver.cluster.local/10.177.177.35 apiserver.cluster.local/g /etc/hosts
```
* k8s-master-03
```shell
echo "10.177.177.34 apiserver.cluster.local" >> /etc/hosts
#加入集群
kubeadm join apiserver.cluster.local:6443 --token 9xl3oj.xwpls6jft9tov81o \
    --discovery-token-ca-cert-hash sha256:3aac728342ab0e2993b0cf7abd13ae703eb032828e26121ce7536d58083ad66f \
    --control-plane --certificate-key 753b1037287af5711a9b12678983a6141da93ec5911f6ea868081b6caaff7ffd
#创建使用命令权限
mkdir ~/.kube && cp /etc/kubernetes/admin.conf ~/.kube/config
#将apiserver.cluster.local映射到本机ip
sed -i s/10.177.177.34 apiserver.cluster.local/10.177.177.35 apiserver.cluster.local/g /etc/hosts
```
* k8s-node-01
```shell
echo "10.177.177.39 apiserver.cluster.local" >> /etc/hosts
kubeadm join apiserver.cluster.local:6443 --token 9xl3oj.xwpls6jft9tov81o\
    --master 10.177.177.34:6443 \
    --master 10.177.177.35:6443 \
    --master 10.177.177.36:6443 \
 --discovery-token-ca-cert-hash sha256:3aac728342ab0e2993b0cf7abd13ae703eb032828e26121ce7536d58083ad66f

 ```
* k8s-node-02
```shell
echo "10.177.177.39 apiserver.cluster.local" >> /etc/hosts
kubeadm join apiserver.cluster.local:6443 --token 9xl3oj.xwpls6jft9tov81o\
    --master 10.177.177.34:6443 \
    --master 10.177.177.35:6443 \
    --master 10.177.177.36:6443 \
 --discovery-token-ca-cert-hash sha256:3aac728342ab0e2993b0cf7abd13ae703eb032828e26121ce7536d58083ad66f
 ```
* 查看集群状态
```shell
kubectl get node
 ```

* 取消污点
> 默认安装完主节点是不能被分配除了系统插件之外的pod的

```shell
kubectl taint nodes k8s-master-01 node-role.kubernetes.io/master-

kubectl taint nodes k8s-master-02 node-role.kubernetes.io/master-

kubectl taint nodes k8s-master-03 node-role.kubernetes.io/master-
```

* 添加污点

> 希望某节点尽量不要被分配任务时添加污点

kubectl taint nodes k8s-master-01 node-role.kubernetes.io/master=:PreferNoSchedule
# Dashboard
* 安装dashboard
```shell
kubectl create -f soft/dashboard/.
```
* 访问dashboard

> https://10.177.177.34:32500

* 生成访问key
```shell
kubectl create serviceaccount dashboard-admin -n kube-system
kubectl create clusterrolebinding dashboard-cluster-admin --clusterrole=cluster-admin --serviceaccount=kube-system:dashboard-admin
kubectl get secret -n kube-system
kubectl describe secret dashboard-admin-token-mnzgv -n kube-system
```
token下的一长串就是秘钥

# ceph集群
* 安装ceph集群

> 安装ceph集群首先确定每台节点有已添加未格式化的硬盘,会自动给每块硬盘创建osd,安装需要等待几分钟

```shell
kubectl create -f  pv/ceph/rook/.
 ```
* 查看ceph集群状态
```shell
kubectl exec -it rook-ceph-operator-7f7cf9bbff-9gwst  -n rook-ceph ceph status
```
* 设置默认storageclass存储
> 设置默认storageclass存储,这样pvc模版在申请pv时不用配storageclass name

```shell
kubectl patch storageclass rook-ceph-block -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
```