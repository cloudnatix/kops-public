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
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-06-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
	"k8s.io/kops/pkg/apis/kops"
)

func TestFindEtcdStatus(t *testing.T) {
	clusterName := "my-cluster"
	c := &azureCloudImplementation{
		tags: map[string]string{
			TagClusterName: clusterName,
		},
	}

	etcdClusterName := "main"
	disks := []compute.Disk{
		{
			Name: to.StringPtr("d0"),
			Tags: map[string]*string{
				TagClusterName:                             to.StringPtr(clusterName),
				TagNameRolePrefix + TagRoleMaster:          to.StringPtr("1"),
				TagNameEtcdClusterPrefix + etcdClusterName: to.StringPtr("a/a,b,c"),
			},
		},
		{
			Name: to.StringPtr("d1"),
			Tags: map[string]*string{
				TagClusterName:                             to.StringPtr(clusterName),
				TagNameRolePrefix + TagRoleMaster:          to.StringPtr("1"),
				TagNameEtcdClusterPrefix + etcdClusterName: to.StringPtr("b/a,b,c"),
			},
		},
		{
			Name: to.StringPtr("d2"),
			Tags: map[string]*string{
				TagClusterName:                             to.StringPtr(clusterName),
				TagNameRolePrefix + TagRoleMaster:          to.StringPtr("1"),
				TagNameEtcdClusterPrefix + etcdClusterName: to.StringPtr("c/a,b,c"),
			},
		},
		{
			// No etcd tag.
			Name: to.StringPtr("not_relevant"),
			Tags: map[string]*string{
				TagClusterName: to.StringPtr("different_cluster"),
			},
		},
		{
			// No corresponding cluster tag.
			Name: to.StringPtr("not_relevant"),
			Tags: map[string]*string{
				TagClusterName: to.StringPtr("different_cluster"),
			},
		},
	}
	etcdClusters, err := c.findEtcdStatus(disks)
	if err != nil {
		t.Fatalf("unexpected error %s", err)
	}
	if len(etcdClusters) != 1 {
		t.Fatalf("unexpected number of etcd clusters: %d", len(etcdClusters))
	}
	etcdCluster := etcdClusters[0]
	if a, e := "main", etcdCluster.Name; a != e {
		t.Errorf("expected %s, but got %s", e, a)
	}

	actual := map[string]*kops.EtcdMemberStatus{}
	for _, m := range etcdCluster.Members {
		actual[m.Name] = m
	}
	expected := map[string]*kops.EtcdMemberStatus{
		"a": &kops.EtcdMemberStatus{
			Name:     "a",
			VolumeId: "d0",
		},
		"b": &kops.EtcdMemberStatus{
			Name:     "b",
			VolumeId: "d1",
		},
		"c": &kops.EtcdMemberStatus{
			Name:     "c",
			VolumeId: "d2",
		},
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, but got %+v", actual, expected)
	}

}
