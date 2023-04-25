// Copyright (c) 2022 Tigera, Inc. All rights reserved.

package runtimesecurity

import (
	"fmt"
	"strings"

	operatorv1 "github.com/tigera/operator/api/v1"
	"github.com/tigera/operator/pkg/components"
	"github.com/tigera/operator/pkg/render"
	relasticsearch "github.com/tigera/operator/pkg/render/common/elasticsearch"
	rmeta "github.com/tigera/operator/pkg/render/common/meta"
	"github.com/tigera/operator/pkg/render/common/secret"
	"github.com/tigera/operator/pkg/tls/certificatemanagement"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	NameSpaceRuntimeSecurity             = "tigera-runtime-security"
	ElasticsearchSashaJobUserSecretName  = "tigera-ee-sasha-elasticsearch-access"
	SashaName                            = "sasha"
	ResourceSashaDefaultCPULimit         = "2"
	ResourceSashaDefaultMemoryLimit      = "1Gi"
	ResourceSashaDefaultCPURequest       = "100m"
	ResourceSashaDefaultMemoryRequest    = "100Mi"
	SashaVerifyAuthVolumeName            = "cc-client-credentials"
	SashaVerifyAuthPath                  = "/var/run/calico-cloud/api"
	SashaVerifyAuthFile                  = "/var/run/calico-cloud/api/clientCredentials.yaml"
	SashaVerifyAuthURL                   = "https://sasha-verify.dev.calicocloud.io"
	ThreatIdName                         = "threat-id"
	ResourceThreatIdDefaultCPULimit      = "1"
	ResourceThreatIdDefaultMemoryLimit   = "1Gi"
	ResourceThreatIdDefaultCPURequest    = "100m"
	ResourceThreatIdDefaultMemoryRequest = "100Mi"
	SashaHistoryVolumeName               = "history"
	SashaHistoryVolumeSizeLimit          = "100Mi"
	SashaHistoryVolumeMountPath          = "/history"
	SashaHistoryRetentionPeriod          = "6h"
)

func RuntimeSecurity(config *Config) render.Component {
	return &component{config: config}
}

// Config contains all the config information RuntimeSecurity needs to render component.
type Config struct {
	// Required config.
	PullSecrets         []*corev1.Secret
	Installation        *operatorv1.InstallationSpec
	OsType              rmeta.OSType
	SashaESSecrets      []*corev1.Secret
	ESClusterConfig     *relasticsearch.ClusterConfig
	ESSecrets           []*corev1.Secret
	ClusterDomain       string
	TrustedBundle       certificatemanagement.TrustedBundle
	RuntimeSecuritySpec *operatorv1.RuntimeSecuritySpec
	// Calculated internal fields.
	sashaImage    string
	threatIdImage string
}

type component struct {
	config *Config
}

func (c *component) ResolveImages(is *operatorv1.ImageSet) error {
	reg := c.config.Installation.Registry
	path := c.config.Installation.ImagePath
	prefix := c.config.Installation.ImagePrefix

	if c.config.OsType != c.SupportedOSType() {
		return fmt.Errorf("sasha is supported only on %s", c.SupportedOSType())
	}

	var err error
	var errMsgs []string

	c.config.sashaImage, err = components.GetReference(components.ComponentSasha, reg, path, prefix, is)
	if err != nil {
		errMsgs = append(errMsgs, err.Error())
	}

	c.config.threatIdImage, err = components.GetReference(components.ComponentThreatId, reg, path, prefix, is)
	if err != nil {
		errMsgs = append(errMsgs, err.Error())
	}

	if len(errMsgs) != 0 {
		return fmt.Errorf(strings.Join(errMsgs, ","))
	}

	return nil
}

func (c *component) Objects() (objsToCreate, objsToDelete []client.Object) {
	var objs, toDelete []client.Object

	objs = append(objs, render.CreateNamespace(NameSpaceRuntimeSecurity, c.config.Installation.KubernetesProvider, render.PSSPrivileged))
	objs = append(objs, secret.ToRuntimeObjects(secret.CopyToNamespace(NameSpaceRuntimeSecurity, c.config.PullSecrets...)...)...)
	objs = append(objs, c.config.TrustedBundle.ConfigMap(NameSpaceRuntimeSecurity))

	if len(c.config.SashaESSecrets) > 0 {
		objs = append(objs, secret.ToRuntimeObjects(secret.CopyToNamespace(NameSpaceRuntimeSecurity, c.config.SashaESSecrets...)...)...)
		objs = append(objs, c.sashaServiceAccount())
		objs = append(objs, c.sashaDeployment())
	}

	toDelete = append(toDelete, c.sashaCronJob())
	toDelete = append(toDelete, c.oldSashaServiceAccount())

	return objs, toDelete
}

func (c *component) Ready() bool {
	return true
}

func (c *component) SupportedOSType() rmeta.OSType {
	return rmeta.OSTypeLinux
}

func (c *component) sashaDeployment() *appsv1.Deployment {

	envVars := []corev1.EnvVar{
		{Name: "SASHA_SECRETLOCATION", Value: SashaVerifyAuthFile},
		{Name: "SASHA_HISTORYDIR", Value: SashaHistoryVolumeMountPath},
		{Name: "SASHA_HISTORYRETENTION", Value: SashaHistoryRetentionPeriod},
	}

	rsSecretOptional := false
	numReplica := int32(1)

	// The threat-id API will use this probe for liveness and readiness
	grpcProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"bin/grpc_health_probe-linux-amd64", "-addr", "127.0.0.1:50051"}}},
		PeriodSeconds:    2,
		FailureThreshold: 6,
	}

	historyVolumeSizeLimit := resource.MustParse(SashaHistoryVolumeSizeLimit)

	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      SashaName,
			Namespace: NameSpaceRuntimeSecurity,
			Labels: map[string]string{
				"k8s-app": SashaName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"k8s-app": SashaName,
				},
			},
			Replicas: &numReplica,
			Template: *(relasticsearch.DecorateAnnotations(&corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SashaName,
					Namespace: NameSpaceRuntimeSecurity,
					Labels: map[string]string{
						"k8s-app": SashaName,
					},
					Annotations: c.config.TrustedBundle.HashAnnotations(),
				},
				Spec: corev1.PodSpec{
					NodeSelector: c.config.Installation.ControlPlaneNodeSelector,
					Tolerations:  c.config.Installation.ControlPlaneTolerations,
					Volumes: []corev1.Volume{
						{
							Name: SashaVerifyAuthVolumeName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "tigera-calico-cloud-client-credentials",
									Optional:   &rsSecretOptional,
								},
							},
						},
						{
							Name: SashaHistoryVolumeName,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{
									SizeLimit: &historyVolumeSizeLimit,
								},
							},
						},
						c.config.TrustedBundle.Volume(),
					},
					Containers: []corev1.Container{
						relasticsearch.ContainerDecorate(corev1.Container{
							Name:      SashaName,
							Image:     c.config.sashaImage,
							Env:       envVars,
							Resources: *c.config.RuntimeSecuritySpec.Sasha.Resources,
							VolumeMounts: append(
								c.config.TrustedBundle.VolumeMounts(c.SupportedOSType()),
								corev1.VolumeMount{
									Name:      SashaVerifyAuthVolumeName,
									MountPath: SashaVerifyAuthPath,
								},
								corev1.VolumeMount{
									Name:      SashaHistoryVolumeName,
									MountPath: SashaHistoryVolumeMountPath,
								},
							),
						},
							c.config.ESClusterConfig.ClusterName(),
							ElasticsearchSashaJobUserSecretName,
							c.config.ClusterDomain,
							c.config.OsType),
						{
							Name:           ThreatIdName,
							Image:          c.config.threatIdImage,
							Resources:      *c.config.RuntimeSecuritySpec.ThreatId.Resources,
							LivenessProbe:  grpcProbe,
							ReadinessProbe: grpcProbe,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      SashaHistoryVolumeName,
									MountPath: SashaHistoryVolumeMountPath,
								},
							},
						},
					},
					ImagePullSecrets:   secret.GetReferenceList(c.config.PullSecrets),
					ServiceAccountName: SashaName,
				},
			}, c.config.ESClusterConfig, c.config.ESSecrets).(*corev1.PodTemplateSpec)),
		},
	}
}

func (c *component) sashaServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{Kind: rbacv1.ServiceAccountKind, APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: SashaName, Namespace: NameSpaceRuntimeSecurity},
	}
}
