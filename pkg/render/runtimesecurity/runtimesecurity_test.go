// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package runtimesecurity_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	operatorv1 "github.com/tigera/operator/api/v1"
	"github.com/tigera/operator/pkg/common"
	relasticsearch "github.com/tigera/operator/pkg/render/common/elasticsearch"
	rmeta "github.com/tigera/operator/pkg/render/common/meta"
	rtest "github.com/tigera/operator/pkg/render/common/test"
	"github.com/tigera/operator/pkg/render/runtimesecurity"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Runtime Security rendering tests", func() {
	var installation *operatorv1.InstallationSpec

	BeforeEach(func() {
		installation = &operatorv1.InstallationSpec{
			KubernetesProvider: operatorv1.ProviderNone,
		}
	})

	It("should render with default Runtime Security configuration", func() {
		esConfig := relasticsearch.NewClusterConfig("cluster", 1, 1, 1)

		sashaSecret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      runtimesecurity.ElasticsearchSashaJobUserSecretName,
				Namespace: common.OperatorNamespace(),
			},
			Data: map[string][]byte{
				"fake": []byte("fake"),
			},
		}

		component := runtimesecurity.RuntimeSecurity(&runtimesecurity.Config{
			PullSecrets:     nil,
			Installation:    installation,
			OsType:          rmeta.OSTypeLinux,
			SashaESSecrets:  []*corev1.Secret{sashaSecret},
			ESClusterConfig: esConfig,
			ClusterDomain:   "nil",
		})

		expectedResources := []struct {
			name    string
			ns      string
			group   string
			version string
			kind    string
		}{
			{name: runtimesecurity.NameSpaceRuntimeSecurity, ns: "", group: "", version: "v1", kind: "Namespace"},
			{name: runtimesecurity.ElasticsearchSashaJobUserSecretName, ns: runtimesecurity.NameSpaceRuntimeSecurity, group: "", version: "v1", kind: "Secret"},
			{name: runtimesecurity.SashaName, ns: runtimesecurity.NameSpaceRuntimeSecurity, group: "", version: "v1", kind: "ServiceAccount"},
			{name: runtimesecurity.SashaName, ns: runtimesecurity.NameSpaceRuntimeSecurity, group: "apps", version: "v1", kind: "Deployment"},
		}

		resources, _ := component.Objects()
		Expect(component.ResolveImages(nil)).To(BeNil())
		Expect(len(resources)).To(Equal(len(expectedResources)))

		i := 0
		for _, expectedRes := range expectedResources {
			rtest.ExpectResource(resources[i], expectedRes.name, expectedRes.ns, expectedRes.group, expectedRes.version, expectedRes.kind)
			i++
		}

		// Check rendering of spec deployment.
		deploy := rtest.GetResource(resources, runtimesecurity.SashaName, runtimesecurity.NameSpaceRuntimeSecurity,
			"apps", "v1", "Deployment").(*appsv1.Deployment)
		spec := deploy.Spec.Template.Spec
		Expect(len(spec.Containers)).To(Equal(2))

		// Basic checks for the liveness and readiness probes for the gRPC API
		threatIdContainer := spec.Containers[1]
		checkThreatIdProbe(threatIdContainer.LivenessProbe)
		checkThreatIdProbe(threatIdContainer.ReadinessProbe)
	})

})

func checkThreatIdProbe(probe *corev1.Probe) {
	Expect(probe.Exec.Command).To(ContainElement("bin/grpc_health_probe-linux-amd64"))
}
