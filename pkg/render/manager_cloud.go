// Copyright (c) 2022-2023 Tigera, Inc. All rights reserved.

package render

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v3 "github.com/tigera/api/pkg/apis/projectcalico/v3"
	"github.com/tigera/operator/pkg/render/common/cloudrbac"
	"github.com/tigera/operator/pkg/render/common/configmap"
	rcimageassurance "github.com/tigera/operator/pkg/render/common/imageassurance"
	rmeta "github.com/tigera/operator/pkg/render/common/meta"
	"github.com/tigera/operator/pkg/render/common/networkpolicy"
	"github.com/tigera/operator/pkg/render/common/secret"
)

var (
	CloudManagerConfigOverrideName = "cloud-manager-config"
	ManagerExtraEnv                = map[string]string{}
)

const (
	ImageAssurancePolicyName = networkpolicy.TigeraComponentPolicyPrefix + "image-assurance-access"
	CloudRBACPPIPolicyName   = networkpolicy.TigeraComponentPolicyPrefix + "cloud-rbac-api"
)

var ImageAssuranceEntityRule = networkpolicy.CreateEntityRule("tigera-image-assurance", "tigera-image-assurance-api-proxy", 5557)

// ManagerCloudResources contains all the resource needed for cloud manager.
type ManagerCloudResources struct {
	TenantID                string
	CloudRBACResources      *cloudrbac.Resources
	ImageAssuranceResources *rcimageassurance.Resources
}

func (c *managerComponent) decorateCloudVoltronContainer(container corev1.Container) corev1.Container {
	// if image assurance is enabled add env needed for it.
	if c.cfg.CloudResources.ImageAssuranceResources != nil {
		container.Env = append(container.Env,
			corev1.EnvVar{Name: "VOLTRON_ENABLE_IMAGE_ASSURANCE", Value: "true"},
			corev1.EnvVar{Name: "VOLTRON_IMAGE_ASSURANCE_CA_BUNDLE_PATH", Value: rcimageassurance.CABundlePath},
			corev1.EnvVar{Name: "VOLTRON_IMAGE_ASSURANCE_ENDPOINT", Value: rcimageassurance.APIEndpoint},
		)
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				MountPath: rcimageassurance.CAMountPath,
				Name:      rcimageassurance.ImageAssuranceSecretName,
				ReadOnly:  true,
			},
		)
	}

	if c.cfg.CloudResources.CloudRBACResources != nil {
		container.Env = append(container.Env,
			corev1.EnvVar{Name: "VOLTRON_ENABLE_CALICO_CLOUD_RBAC_API", Value: "true"},
			corev1.EnvVar{Name: "VOLTRON_CALICO_CLOUD_RBAC_API_CA_BUNDLE_PATH", Value: cloudrbac.CABundlePath},
			corev1.EnvVar{Name: "VOLTRON_CALICO_CLOUD_RBAC_API_ENDPOINT", Value: cloudrbac.APIEndpoint},
		)
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				MountPath: cloudrbac.CAMountPath,
				Name:      cloudrbac.TLSSecretName,
				ReadOnly:  true,
			},
		)
	}

	return container
}

func (c *managerComponent) decorateCloudDeploymentSpec(templateSpec corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	// if image assurance is enabled add env needed for it.
	if c.cfg.CloudResources.ImageAssuranceResources != nil {
		templateSpec.ObjectMeta.Annotations[rcimageassurance.ImageAssuranceCertHashAnnotation] = rmeta.AnnotationHash(c.cfg.CloudResources.ImageAssuranceResources.TLSSecret.Data)
		templateSpec.Spec.Volumes = append(templateSpec.Spec.Volumes,
			corev1.Volume{
				Name: rcimageassurance.ImageAssuranceSecretName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						Items: []corev1.KeyToPath{{
							Key:  "tls.crt",
							Path: "tls.crt",
						}},
						SecretName: rcimageassurance.ImageAssuranceSecretName,
					},
				},
			})
	}

	cloudRBACResources := c.cfg.CloudResources.CloudRBACResources
	if cloudRBACResources != nil {
		templateSpec.ObjectMeta.Annotations[cloudrbac.CertHashAnnotation] = rmeta.AnnotationHash(cloudRBACResources.TLSSecret.Data)
		templateSpec.Spec.Volumes = append(templateSpec.Spec.Volumes,
			corev1.Volume{
				Name: cloudrbac.TLSSecretName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: cloudrbac.TLSSecretName,
					},
				},
			})
	}

	return templateSpec
}

func (c *managerComponent) addCloudResources(objs []client.Object) []client.Object {
	// if image assurance is enabled add corresponding resources.
	if c.cfg.CloudResources.ImageAssuranceResources != nil {
		objs = append(objs, secret.ToRuntimeObjects(&corev1.Secret{
			TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: rcimageassurance.ImageAssuranceSecretName, Namespace: ManagerNamespace},
			Data: map[string][]byte{
				corev1.TLSCertKey: c.cfg.CloudResources.ImageAssuranceResources.TLSSecret.Data[corev1.TLSCertKey],
			},
		})...)

		objs = append(objs, configmap.ToRuntimeObjects(configmap.CopyToNamespace(ManagerNamespace,
			c.cfg.CloudResources.ImageAssuranceResources.ConfigurationConfigMap)...)...)

		objs = append(objs, c.managerImageAssuranceNetworkPolicy())
	}

	cloudRBACResources := c.cfg.CloudResources.CloudRBACResources
	if cloudRBACResources != nil {
		objs = append(objs, &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: cloudrbac.TLSSecretName, Namespace: ManagerNamespace},
			Data: map[string][]byte{
				corev1.TLSCertKey: cloudRBACResources.TLSSecret.Data[corev1.TLSCertKey],
			},
		})

		objs = append(objs, c.managerToCloudRBACAPINetworkPolicy(cloudRBACResources))
	}

	return objs
}

// Do this as a separate function to try to make updates in the future easier.
func (c *managerComponent) setManagerCloudEnvs(envs []corev1.EnvVar) []corev1.EnvVar {
	envs = append(envs,
		corev1.EnvVar{Name: "ENABLE_MANAGED_CLUSTERS_ONLY", Value: "true"},
		corev1.EnvVar{Name: "LICENSE_EDITION", Value: "cloudEdition"},
	)

	// extra cloud specific env vars needed for image assurance
	if c.cfg.CloudResources.ImageAssuranceResources != nil {
		envs = append(envs,
			corev1.EnvVar{Name: "ENABLE_IMAGE_ASSURANCE_SUPPORT", Value: "true"},
			corev1.EnvVar{Name: "CNX_IMAGE_ASSURANCE_API_URL", Value: "/bast/v1"},
			corev1.EnvVar{Name: "CNX_IMAGE_ASSURANCE_ORGANIZATION_ID",
				ValueFrom: &corev1.EnvVarSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: rcimageassurance.ConfigurationConfigMapName},
						Key:                  rcimageassurance.ConfigurationConfigMapOrgIDKey,
					},
				},
			},
			corev1.EnvVar{Name: "IMAGE_ASSURANCE_SCANNER_CLI_TOKEN_SECRET_NAME", Value: rcimageassurance.ScannerCLITokenSecretName},
			corev1.EnvVar{Name: "IMAGE_ASSURANCE_SCANNER_CLI_DOWNLOAD_URL", Value: rcimageassurance.ScannerCLIDownloadURL},
		)
	}

	// move extra env vars into Manager, but sort them alphabetically first,
	// otherwise, since map iteration is random, they'll be added to the env vars in a random order,
	// which will cause another reconciliation event when Manager is updated.
	sortedKeys := []string{}
	for k := range ManagerExtraEnv {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	for _, key := range sortedKeys {
		val := ManagerExtraEnv[key]
		if key == "portalAPIURL" {
			// support legacy functionality where 'portalAPIURL' was a special field used to set
			// the portal url and enable support.
			envs = append(envs,
				corev1.EnvVar{Name: "CNX_PORTAL_URL", Value: val},
				corev1.EnvVar{Name: "ENABLE_PORTAL_SUPPORT", Value: "true"})
			continue
		}

		if key == "auth0OrgID" {
			// support legacy functionality where 'auth0OrgID' was a special field used to set
			// the org ID
			envs = append(envs, corev1.EnvVar{Name: "CNX_AUTH0_ORG_ID", Value: val})
			continue
		}

		envs = append(envs, corev1.EnvVar{Name: key, Value: val})
	}

	return envs
}

func (c *managerComponent) managerImageAssuranceNetworkPolicy() *v3.NetworkPolicy {
	egressRules := []v3.Rule{
		{
			Action:      v3.Allow,
			Protocol:    &networkpolicy.TCPProtocol,
			Destination: ImageAssuranceEntityRule,
		},
	}

	return &v3.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{Kind: "NetworkPolicy", APIVersion: "projectcalico.org/v3"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ImageAssurancePolicyName,
			Namespace: ManagerNamespace,
		},
		Spec: v3.NetworkPolicySpec{
			Order:    &networkpolicy.HighPrecedenceOrder,
			Tier:     networkpolicy.TigeraComponentTierName,
			Selector: networkpolicy.KubernetesAppSelector(ManagerDeploymentName),
			Types:    []v3.PolicyType{v3.PolicyTypeEgress},
			Egress:   egressRules,
		},
	}
}

func (c *managerComponent) managerToCloudRBACAPINetworkPolicy(rbacResources *cloudrbac.Resources) *v3.NetworkPolicy {
	return &v3.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{Kind: "NetworkPolicy", APIVersion: "projectcalico.org/v3"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CloudRBACPPIPolicyName,
			Namespace: ManagerNamespace,
		},
		Spec: v3.NetworkPolicySpec{
			Order:    &networkpolicy.HighPrecedenceOrder,
			Tier:     networkpolicy.TigeraComponentTierName,
			Selector: networkpolicy.KubernetesAppSelector(ManagerDeploymentName),
			Types:    []v3.PolicyType{v3.PolicyTypeEgress},
			Egress: []v3.Rule{
				{
					Action:      v3.Allow,
					Protocol:    &networkpolicy.TCPProtocol,
					Destination: networkpolicy.CreateServiceSelectorEntityRule(rbacResources.NamespaceName, rbacResources.ServiceName),
				},
			},
		},
	}
}
