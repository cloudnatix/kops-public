/*
Copyright 2020 The Kubernetes Authors.

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

package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-06-01/compute"
	"k8s.io/klog"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/protokube/pkg/etcd"
	"k8s.io/kops/upup/pkg/fi"
)

// FindClusterStatus discovers the status of the cluster by looking for the tagged etcd volumes.
func (c *azureCloudImplementation) FindClusterStatus(cluster *kops.Cluster) (*kops.ClusterStatus, error) {
	klog.V(2).Infof("Listing Azure managed disks.")
	disks, err := c.Disk().List(context.TODO(), cluster.Spec.Azure.ResourceGroupName)
	if err != nil {
		return nil, fmt.Errorf("error listing disks: %s", err)
	}

	etcdStatus, err := c.findEtcdStatus(disks)
	if err != nil {
		return nil, err
	}
	status := &kops.ClusterStatus{
		EtcdClusters: etcdStatus,
	}
	klog.V(2).Infof("Cluster status (from cloud): %v", fi.DebugAsJsonString(status))
	return status, nil
}

func (c *azureCloudImplementation) findEtcdStatus(disks []compute.Disk) ([]kops.EtcdClusterStatus, error) {
	statusMap := make(map[string]*kops.EtcdClusterStatus)
	for _, disk := range disks {
		if !c.isDiskForCluster(&disk) {
			continue
		}

		var (
			etcdClusterName string
			etcdClusterSpec *etcd.EtcdClusterSpec
			master          bool
		)
		for k, v := range disk.Tags {
			if k == TagNameRolePrefix+TagRoleMaster {
				master = true
				continue
			}

			if strings.HasPrefix(k, TagNameEtcdClusterPrefix) {
				etcdClusterName = strings.TrimPrefix(k, TagNameEtcdClusterPrefix)
				var err error
				etcdClusterSpec, err = etcd.ParseEtcdClusterSpec(etcdClusterName, *v)
				if err != nil {
					return nil, fmt.Errorf("error parsing etcd cluster tag %q on volume %q: %s", *v, *disk.Name, err)
				}
			}
		}

		if etcdClusterName == "" || etcdClusterSpec == nil || !master {
			continue
		}

		status := statusMap[etcdClusterName]
		if status == nil {
			status = &kops.EtcdClusterStatus{
				Name: etcdClusterName,
			}
			statusMap[etcdClusterName] = status
		}
		status.Members = append(status.Members, &kops.EtcdMemberStatus{
			Name:     etcdClusterSpec.NodeName,
			VolumeId: *disk.Name,
		})
	}

	var status []kops.EtcdClusterStatus
	for _, v := range statusMap {
		status = append(status, *v)
	}
	return status, nil
}

// isDiskForCluster returns true if the managed disk is for the cluster.
func (c *azureCloudImplementation) isDiskForCluster(disk *compute.Disk) bool {
	found := 0
	for k, v := range disk.Tags {
		if c.tags[k] == *v {
			found++
		}
	}
	return found == len(c.tags)
}
