/*
Copyright 2016 The Rook Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package installer

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/rook/rook/pkg/daemon/ceph/client"
	opspec "github.com/rook/rook/pkg/operator/ceph/spec"
	"github.com/rook/rook/tests/framework/utils"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// test with the latest luminous build
	luminousTestImage = "ceph/ceph:v12"
	// test with the latest mimic build
	mimicTestImage = "ceph/ceph:v13"
	// test with the latest nautilus build
	nautilusTestImage = "ceph/ceph:v14.2.1-20190430"
	helmChartName     = "local/rook-ceph"
	helmDeployName    = "rook-ceph"
)

var (
	LuminousVersion = cephv1.CephVersionSpec{Image: luminousTestImage}
	MimicVersion    = cephv1.CephVersionSpec{Image: mimicTestImage}
	NautilusVersion = cephv1.CephVersionSpec{Image: nautilusTestImage}
)

// CephInstaller wraps installing and uninstalling rook on a platform
type CephInstaller struct {
	Manifests        CephManifests
	k8shelper        *utils.K8sHelper
	hostPathToDelete string
	helmHelper       *utils.HelmHelper
	useHelm          bool
	k8sVersion       string
	changeHostnames  bool
	CephVersion      cephv1.CephVersionSpec
	T                func() *testing.T
}

func (h *CephInstaller) CreateCephCRDs() error {
	var resources string
	logger.Info("Creating Rook CRDs")

	resources = h.Manifests.GetRookCRDs()

	var err error
	for i := 0; i < 5; i++ {
		if i > 0 {
			logger.Infof("waiting 10s...")
			time.Sleep(10 * time.Second)
		}

		_, err = h.k8shelper.KubectlWithStdin(resources, createFromStdinArgs...)
		if err == nil {
			return nil
		}

		// If the CRD already exists, the previous test must not have completed cleanup yet.
		// Delete the CRDs and attempt to wait for the cleanup.
		if strings.Index(err.Error(), "AlreadyExists") == -1 {
			return err
		}

		// ensure all the cluster CRDs are removed
		if err = h.purgeClusters(); err != nil {
			logger.Warningf("could not purge cluster crds. %+v", err)
		}

		// remove the finalizer from the cluster CRD
		if _, err := h.k8shelper.Kubectl("patch", "crd", "cephclusters.ceph.rook.io", "-p", `{"metadata":{"finalizers": []}}`, "--type=merge"); err != nil {
			logger.Warningf("could not remove finalizer from cluster crd. %+v", err)
		}

		logger.Warningf("CRDs were not cleaned up from a previous test. Deleting them to try again...")
		if _, err := h.k8shelper.KubectlWithStdin(resources, deleteFromStdinArgs...); err != nil {
			logger.Infof("deleting the crds returned an error: %+v", err)
		}
	}

	return err
}

// CreateCephOperator creates rook-operator via kubectl
func (h *CephInstaller) CreateCephOperator(namespace string) (err error) {
	logger.Infof("Starting Rook Operator")
	// creating clusterrolebinding for kubeadm env.
	h.k8shelper.CreateAnonSystemClusterBinding()

	// creating rook resources
	if err = h.CreateCephCRDs(); err != nil {
		return err
	}

	if h.changeHostnames {
		// give nodes a hostname that is different from its k8s node name to confirm that all the daemons will be initialized properly
		h.k8shelper.ChangeHostnames()
	}

	rookOperator := h.Manifests.GetRookOperator(namespace)

	_, err = h.k8shelper.KubectlWithStdin(rookOperator, createFromStdinArgs...)
	if err != nil {
		return fmt.Errorf("Failed to create rook-operator pod : %v ", err)
	}

	logger.Infof("Rook Operator started")

	return nil
}

// CreateK8sRookOperatorViaHelm creates rook operator via Helm chart named local/rook present in local repo
func (h *CephInstaller) CreateK8sRookOperatorViaHelm(namespace string) error {
	// creating clusterrolebinding for kubeadm env.
	h.k8shelper.CreateAnonSystemClusterBinding()

	helmTag, err := h.helmHelper.GetLocalRookHelmChartVersion(helmChartName)

	if err != nil {
		return fmt.Errorf("Failed to get Version of helm chart %v, err : %v", helmChartName, err)
	}

	err = h.helmHelper.InstallLocalRookHelmChart(helmChartName, helmDeployName, helmTag, namespace)
	if err != nil {
		return fmt.Errorf("failed to install rook operator via helm, err : %v", err)

	}

	return nil
}

// CreateK8sRookToolbox creates rook-ceph-tools via kubectl
func (h *CephInstaller) CreateK8sRookToolbox(namespace string) (err error) {
	logger.Infof("Starting Rook toolbox")

	rookToolbox := h.Manifests.GetRookToolBox(namespace)

	_, err = h.k8shelper.KubectlWithStdin(rookToolbox, createFromStdinArgs...)

	if err != nil {
		return fmt.Errorf("Failed to create rook-toolbox pod : %v ", err)
	}

	if !h.k8shelper.IsPodRunning("rook-ceph-tools", namespace) {
		return fmt.Errorf("Rook Toolbox couldn't start")
	}
	logger.Infof("Rook Toolbox started")

	return nil
}

func (h *CephInstaller) CreateK8sRookCluster(namespace, systemNamespace string, storeType string) (err error) {
	return h.CreateK8sRookClusterWithHostPathAndDevices(namespace, systemNamespace, storeType, false,
		cephv1.MonSpec{Count: 3, AllowMultiplePerNode: true}, true, /* startWithAllNodes */
		1, /* rbd workers */
		LuminousVersion)
}

// CreateK8sRookCluster creates rook cluster via kubectl
func (h *CephInstaller) CreateK8sRookClusterWithHostPathAndDevices(namespace, systemNamespace, storeType string,
	useAllDevices bool, mon cephv1.MonSpec, startWithAllNodes bool, rbdMirrorWorkers int, cephVersion cephv1.CephVersionSpec) error {

	dataDirHostPath, err := h.initTestDir(namespace)
	if err != nil {
		return fmt.Errorf("failed to create test dir. %+v", err)
	}
	logger.Infof("Creating cluster: namespace=%s, systemNamespace=%s, storeType=%s, dataDirHostPath=%s, useAllDevices=%t, startWithAllNodes=%t, mons=%+v",
		namespace, systemNamespace, storeType, dataDirHostPath, useAllDevices, startWithAllNodes, mon)

	logger.Infof("Creating namespace %s", namespace)
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	_, err = h.k8shelper.Clientset.CoreV1().Namespaces().Create(ns)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %s. %+v", namespace, err)
	}

	// Skip this step since the helm chart already includes the roles and bindings
	if !h.useHelm {
		logger.Infof("Creating cluster roles")
		roles := h.Manifests.GetClusterRoles(namespace, systemNamespace)
		if _, err := h.k8shelper.KubectlWithStdin(roles, createFromStdinArgs...); err != nil {
			return fmt.Errorf("Failed to create cluster roles. %+v", err)
		}
	}

	logger.Infof("Starting Rook Cluster with yaml")
	settings := &ClusterSettings{namespace, storeType, dataDirHostPath, useAllDevices, mon.Count, rbdMirrorWorkers, cephVersion}
	rookCluster := h.Manifests.GetRookCluster(settings)
	if _, err := h.k8shelper.KubectlWithStdin(rookCluster, createFromStdinArgs...); err != nil {
		return fmt.Errorf("Failed to create rook cluster : %v ", err)
	}

	if err := h.k8shelper.WaitForPodCount("app=rook-ceph-mon", namespace, mon.Count); err != nil {
		return err
	}

	if err := h.k8shelper.WaitForPodCount("app=rook-ceph-osd", namespace, 1); err != nil {
		return err
	}

	if rbdMirrorWorkers > 0 {
		if err := h.k8shelper.WaitForPodCount("app=rook-ceph-rbd-mirror", namespace, rbdMirrorWorkers); err != nil {
			return err
		}
	}

	logger.Infof("Rook Cluster started")
	err = h.k8shelper.WaitForLabeledPodsToRun("app=rook-ceph-osd", namespace)
	return err
}

func (h *CephInstaller) initTestDir(namespace string) (string, error) {
	h.hostPathToDelete = path.Join(baseTestDir, "rook-test")
	testDir := path.Join(h.hostPathToDelete, namespace)

	if createBaseTestDir {
		// Create the test dir on the local host
		if err := os.MkdirAll(testDir, 0777); err != nil {
			return "", err
		}

		var err error
		if testDir, err = ioutil.TempDir(testDir, "test-"); err != nil {
			return "", err
		}
	} else {
		// Compose a random test directory name without actually creating it since not running on the localhost
		r := rand.Int()
		testDir = path.Join(testDir, fmt.Sprintf("test-%d", r))
	}
	return testDir, nil
}

func (h *CephInstaller) GetNodeHostnames() ([]string, error) {
	nodes, err := h.k8shelper.Clientset.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s nodes. %+v", err)
	}
	var names []string
	for _, node := range nodes.Items {
		names = append(names, node.Labels[v1.LabelHostname])
	}

	return names, nil
}

// InstallRookOnK8sWithHostPathAndDevices installs rook on k8s
func (h *CephInstaller) InstallRookOnK8sWithHostPathAndDevices(namespace, storeType string,
	useDevices bool, mon cephv1.MonSpec, startWithAllNodes bool, rbdMirrorWorkers int) (bool, error) {

	var err error
	// flag used for local debuggin purpose, when rook is pre-installed
	if Env.SkipInstallRook {
		return true, nil
	}

	k8sversion := h.k8shelper.GetK8sServerVersion()

	logger.Infof("Installing rook on k8s %s", k8sversion)

	onamespace := namespace
	// Create rook operator
	if h.useHelm {
		err = h.CreateK8sRookOperatorViaHelm(namespace)
		if err != nil {
			logger.Errorf("Rook Operator not installed ,error -> %v", err)
			return false, err

		}
	} else {
		onamespace = SystemNamespace(namespace)

		err := h.CreateCephOperator(onamespace)
		if err != nil {
			logger.Errorf("Rook Operator not installed ,error -> %v", err)
			return false, err
		}
	}
	if !h.k8shelper.IsPodInExpectedState("rook-ceph-operator", onamespace, "Running") {
		logger.Error("rook-ceph-operator is not running")
		h.k8shelper.GetLogs("rook-ceph-operator", Env.HostType, onamespace, "test-setup")
		logger.Error("rook-ceph-operator is not Running, abort!")
		return false, err
	}

	if forceUseDevices {
		logger.Infof("Forcing the use of devices")
		useDevices = true
	} else if useDevices {
		// This check only looks at the local machine for devices. If you want to force using devices,
		// set the forceUseDevices flag
		useDevices = IsAdditionalDeviceAvailableOnCluster()
	}

	// Create rook cluster
	err = h.CreateK8sRookClusterWithHostPathAndDevices(namespace, onamespace, storeType,
		useDevices, cephv1.MonSpec{Count: mon.Count, AllowMultiplePerNode: mon.AllowMultiplePerNode}, startWithAllNodes,
		rbdMirrorWorkers, h.CephVersion)
	if err != nil {
		logger.Errorf("Rook cluster %s not installed, error -> %v", namespace, err)
		return false, err
	}

	// Create rook client
	err = h.CreateK8sRookToolbox(namespace)
	if err != nil {
		logger.Errorf("Rook toolbox in cluster %s not installed, error -> %v", namespace, err)
		return false, err
	}
	logger.Infof("installed rook operator and cluster : %s on k8s %s", namespace, h.k8sVersion)
	return true, nil
}

// UninstallRookFromK8s uninstalls rook from k8s
func (h *CephInstaller) UninstallRook(namespace string) {
	h.UninstallRookFromMultipleNS(SystemNamespace(namespace), namespace)
}

// UninstallRookFromK8s uninstalls rook from multiple namespaces in k8s
func (h *CephInstaller) UninstallRookFromMultipleNS(systemNamespace string, namespaces ...string) {
	// flag used for local debugging purpose, when rook is pre-installed
	if Env.SkipInstallRook {
		return
	}

	logger.Infof("Uninstalling Rook")
	var err error
	for _, namespace := range namespaces {
		if h.T().Failed() {
			// When the test has failed, it's sometimes useful to have pod descriptions to check
			// that pods are configured as expected.
			h.k8shelper.PrintPodDescribeForNamespace(namespace)
		} else {
			// if the test passed, check that the ceph status is HEALTH_OK before we tear the cluster down
			h.checkCephHealthStatus(namespace)
		}

		roles := h.Manifests.GetClusterRoles(namespace, systemNamespace)
		_, err = h.k8shelper.KubectlWithStdin(roles, deleteFromStdinArgs...)

		err = h.k8shelper.DeleteResourceAndWait(false, "-n", namespace, "cephcluster", namespace)
		checkError(h.T(), err, fmt.Sprintf("cannot remove cluster %s", namespace))

		crdCheckerFunc := func() error {
			_, err := h.k8shelper.RookClientset.CephV1().CephClusters(namespace).Get(namespace, metav1.GetOptions{})
			return err
		}
		err = h.k8shelper.WaitForCustomResourceDeletion(namespace, crdCheckerFunc)
		checkError(h.T(), err, fmt.Sprintf("failed to wait for crd %s deletion", namespace))

		err = h.k8shelper.DeleteResourceAndWait(false, "namespace", namespace)
		checkError(h.T(), err, fmt.Sprintf("cannot delete namespace %s", namespace))
	}

	logger.Infof("removing the operator from namespace %s", systemNamespace)
	err = h.k8shelper.DeleteResource(
		"crd",
		"cephclusters.ceph.rook.io",
		"cephblockpools.ceph.rook.io",
		"cephobjectstores.ceph.rook.io",
		"cephobjectstoreusers.ceph.rook.io",
		"cephfilesystems.ceph.rook.io",
		"cephnfses.ceph.rook.io",
		"volumes.rook.io")
	checkError(h.T(), err, "cannot delete CRDs")

	if h.useHelm {
		err = h.helmHelper.DeleteLocalRookHelmChart(helmDeployName)
	} else {
		rookOperator := h.Manifests.GetRookOperator(systemNamespace)
		_, err = h.k8shelper.KubectlWithStdin(rookOperator, deleteFromStdinArgs...)
	}
	checkError(h.T(), err, "cannot uninstall rook-operator")

	h.k8shelper.Clientset.RbacV1beta1().RoleBindings(systemNamespace).Delete("rook-ceph-system", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoleBindings().Delete("rook-ceph-global", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoleBindings().Delete("rook-ceph-mgr-cluster", nil)
	h.k8shelper.Clientset.CoreV1().ServiceAccounts(systemNamespace).Delete("rook-ceph-system", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-cluster-mgmt", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-cluster-mgmt-rules", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-mgr-cluster", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-mgr-cluster-rules", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-mgr-system", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-mgr-system-rules", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-global", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rook-ceph-global-rules", nil)
	h.k8shelper.Clientset.RbacV1beta1().Roles(systemNamespace).Delete("rook-ceph-system", nil)

	h.k8shelper.Clientset.RbacV1beta1().ClusterRoleBindings().Delete("rbd-csi-nodeplugin", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rbd-csi-nodeplugin", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rbd-csi-nodeplugin-rules", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoleBindings().Delete("rbd-csi-provisioner-role", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rbd-external-provisioner-runner", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("rbd-external-provisioner-runner-rules", nil)

	h.k8shelper.Clientset.RbacV1beta1().ClusterRoleBindings().Delete("cephfs-csi-nodeplugin", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("cephfs-csi-nodeplugin", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("cephfs-csi-nodeplugin-rules", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoleBindings().Delete("cephfs-csi-provisioner-role", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("cephfs-external-provisioner-runner", nil)
	h.k8shelper.Clientset.RbacV1beta1().ClusterRoles().Delete("cephfs-external-provisioner-runner-rules", nil)

	h.k8shelper.Clientset.CoreV1().ConfigMaps(systemNamespace).Delete("csi-rbd-config", nil)
	h.k8shelper.Clientset.CoreV1().ConfigMaps(systemNamespace).Delete("csi-cephfs-config", nil)

	logger.Infof("done removing the operator from namespace %s", systemNamespace)
	logger.Infof("removing host data dir %s", h.hostPathToDelete)
	// removing data dir if exists
	if h.hostPathToDelete != "" {
		nodes, err := h.GetNodeHostnames()
		checkError(h.T(), err, "cannot get node names")
		for _, node := range nodes {
			err = h.cleanupDir(node, h.hostPathToDelete)
			logger.Infof("removing %s from node %s. err=%v", h.hostPathToDelete, node, err)
		}
	}
	if h.changeHostnames {
		// revert the hostname labels for the test
		h.k8shelper.RestoreHostnames()
	}
}

func (h *CephInstaller) purgeClusters() error {
	// get all namespaces
	namespaces, err := h.k8shelper.Clientset.CoreV1().Namespaces().List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to get namespaces. %+v", err)
	}

	// look for the clusters in all namespaces
	for _, n := range namespaces.Items {
		namespace := n.Name
		logger.Infof("looking in namespace %s for clusters to purge", namespace)
		clusters, err := h.k8shelper.RookClientset.CephV1().CephClusters(namespace).List(metav1.ListOptions{})
		if err != nil {
			logger.Warningf("failed to get clusters in namespace %s. %+v", namespace, err)
			continue
		}
		if len(clusters.Items) > 0 {
			logger.Warningf("FOUND UNEXPECTED CLUSTER IN NAMESPACE %s. Removing...", namespace)
			h.UninstallRook(namespace)
		}
	}
	return nil
}

func (h *CephInstaller) checkCephHealthStatus(namespace string) {
	clusterResource, err := h.k8shelper.RookClientset.CephV1().CephClusters(namespace).Get(namespace, metav1.GetOptions{})
	assert.Nil(h.T(), err)
	assert.Equal(h.T(), "Created", string(clusterResource.Status.State))

	// Depending on the tests, the health may be fluctuating with different components being started or stopped.
	// If needed, give it a few seconds to settle and check the status again.
	if clusterResource.Status.CephStatus.Health != "HEALTH_OK" {
		time.Sleep(10 * time.Second)
		clusterResource, err = h.k8shelper.RookClientset.CephV1().CephClusters(namespace).Get(namespace, metav1.GetOptions{})
		assert.Nil(h.T(), err)
	}

	// The health status is not stable enough for the integration tests to rely on.
	// We should enable this check if we can get the ceph status to be stable despite all the changing configurations performed by rook.
	//assert.Equal(h.T(), "HEALTH_OK", clusterResource.Status.CephStatus.Health)
	assert.NotEqual(h.T(), "", clusterResource.Status.CephStatus.LastChecked)

	// Print the details if the health is not ok
	if clusterResource.Status.CephStatus.Health != "HEALTH_OK" {
		logger.Errorf("Ceph health status: %s", clusterResource.Status.CephStatus.Health)
		for name, message := range clusterResource.Status.CephStatus.Details {
			logger.Errorf("Ceph health message: %s. %s: %s", name, message.Severity, message.Message)
		}
	}
}

func (h *CephInstaller) cleanupDir(node, dir string) error {
	resources := h.Manifests.GetCleanupPod(node, dir)
	_, err := h.k8shelper.KubectlWithStdin(resources, createFromStdinArgs...)
	return err
}

func (h *CephInstaller) GatherAllRookLogs(namespace, systemNamespace string, testName string) {
	logger.Infof("Gathering all logs from Rook Cluster %s", namespace)
	h.k8shelper.GetPreviousLogs("rook-ceph-operator", Env.HostType, systemNamespace, testName)
	h.k8shelper.GetLogs("rook-ceph-operator", Env.HostType, systemNamespace, testName)
	h.k8shelper.GetLogs("rook-ceph-agent", Env.HostType, systemNamespace, testName)
	h.k8shelper.GetLogs("rook-discover", Env.HostType, systemNamespace, testName)
	h.k8shelper.GetLogs("rook-ceph-mgr", Env.HostType, namespace, testName)
	h.k8shelper.GetLogs("rook-ceph-mon", Env.HostType, namespace, testName)
	h.k8shelper.GetLogs("rook-ceph-osd", Env.HostType, namespace, testName)
	h.k8shelper.GetLogs("rook-ceph-osd-prepare", Env.HostType, namespace, testName)
	h.k8shelper.GetLogs("rook-ceph-rgw", Env.HostType, namespace, testName)
	h.k8shelper.GetLogs("rook-ceph-mds", Env.HostType, namespace, testName)
	h.k8shelper.GetContainerLogs("rook-ceph-mgr", Env.HostType, namespace, testName, opspec.ConfigInitContainerName)
	h.k8shelper.GetContainerLogs("rook-ceph-mon", Env.HostType, namespace, testName, opspec.ConfigInitContainerName)
	h.k8shelper.GetContainerLogs("rook-ceph-osd", Env.HostType, namespace, testName, opspec.ConfigInitContainerName)
	h.k8shelper.GetContainerLogs("rook-ceph-rgw", Env.HostType, namespace, testName, opspec.ConfigInitContainerName)
	h.k8shelper.GetContainerLogs("rook-ceph-mds", Env.HostType, namespace, testName, opspec.ConfigInitContainerName)
}

// NewCephInstaller creates new instance of CephInstaller
func NewCephInstaller(t func() *testing.T, clientset *kubernetes.Clientset, useHelm bool, rookVersion string, cephVersion cephv1.CephVersionSpec) *CephInstaller {

	// All e2e tests should run ceph commands in the toolbox since we are not inside a container
	client.RunAllCephCommandsInToolbox = true

	version, err := clientset.ServerVersion()
	if err != nil {
		logger.Infof("failed to get kubectl server version. %+v", err)
	}

	k8shelp, err := utils.CreateK8sHelper(t)
	if err != nil {
		panic("failed to get kubectl client :" + err.Error())
	}
	logger.Infof("Rook Version: %s", rookVersion)
	logger.Infof("Ceph Version: %s", cephVersion.Image)
	h := &CephInstaller{
		Manifests:       NewCephManifests(rookVersion),
		k8shelper:       k8shelp,
		helmHelper:      utils.NewHelmHelper(Env.Helm),
		useHelm:         useHelm,
		k8sVersion:      version.String(),
		CephVersion:     cephVersion,
		changeHostnames: rookVersion != Version0_9 && k8shelp.VersionAtLeast("v1.13.0"),
		T:               t,
	}
	flag.Parse()
	return h
}
