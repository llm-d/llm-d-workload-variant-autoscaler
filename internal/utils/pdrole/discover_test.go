package pdrole

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	epconfigv1alpha1 "sigs.k8s.io/gateway-api-inference-extension/apix/config/v1alpha1"

	poolutil "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/utils/pool"
)

const testNamespace = "test-ns"

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	return scheme
}

func makePool(name, namespace, serviceName string) *poolutil.EndpointPool {
	return &poolutil.EndpointPool{
		Name:      name,
		Namespace: namespace,
		EndpointPicker: &poolutil.EndpointPicker{
			ServiceName:       serviceName,
			Namespace:         namespace,
			MetricsPortNumber: 9090,
		},
	}
}

func makeService(name, namespace string, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    []corev1.ServicePort{{Name: "metrics", Port: 9090}},
		},
	}
}

func makeEPPDeployment(name, namespace string, labels map[string]string, configMapName string) *appsv1.Deployment {
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{},
			},
		},
	}
	if configMapName != "" {
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		}}
	}
	return deploy
}

func makeConfigMap(name, namespace string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       data,
	}
}

func buildConfigJSON(plugins []epconfigv1alpha1.PluginSpec, profiles []epconfigv1alpha1.SchedulingProfile) string {
	epc := epconfigv1alpha1.EndpointPickerConfig{
		Plugins:            plugins,
		SchedulingProfiles: profiles,
	}
	data, _ := json.Marshal(epc)
	return string(data)
}

var _ = Describe("DiscoverPDRoleLabelConfig", func() {
	var (
		ctx       context.Context
		eppLabels map[string]string
		pool      *poolutil.EndpointPool
	)

	BeforeEach(func() {
		ctx = context.Background()
		eppLabels = map[string]string{"app": "epp"}
		pool = makePool("test-pool", testNamespace, "epp-svc")
	})

	Context("full chain with prefill-filter/decode-filter plugins", func() {
		It("should return disaggregated=true with default label config", func() {
			configJSON := buildConfigJSON(
				[]epconfigv1alpha1.PluginSpec{
					{Name: "pf", Type: PluginTypePrefillFilter},
					{Name: "df", Type: PluginTypeDecodeFilter},
				},
				[]epconfigv1alpha1.SchedulingProfile{
					{Name: "prefill", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "pf"}}},
					{Name: "decode", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "df"}}},
				},
			)
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, "epp-config"),
				makeConfigMap("epp-config", testNamespace, map[string]string{"config.yaml": configJSON}),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()

			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disaggregated).To(BeTrue())
			expected := DefaultPDRoleLabelConfig()
			Expect(result.LabelConfig.LabelKey).To(Equal(expected.LabelKey))
			Expect(result.LabelConfig.PrefillValues).To(Equal(expected.PrefillValues))
			Expect(result.LabelConfig.DecodeValues).To(Equal(expected.DecodeValues))
		})
	})

	Context("full chain with by-label plugins", func() {
		It("should return disaggregated=true with custom label config", func() {
			prefillParams, _ := json.Marshal(byLabelParams{Label: "custom.io/role", ValidValues: []string{"pf"}})
			decodeParams, _ := json.Marshal(byLabelParams{Label: "custom.io/role", ValidValues: []string{"dc", "mixed"}})

			configJSON := buildConfigJSON(
				[]epconfigv1alpha1.PluginSpec{
					{Name: "pf-filter", Type: PluginTypeByLabel, Parameters: prefillParams},
					{Name: "dc-filter", Type: PluginTypeByLabel, Parameters: decodeParams},
				},
				[]epconfigv1alpha1.SchedulingProfile{
					{Name: "prefill", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "pf-filter"}}},
					{Name: "decode", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "dc-filter"}}},
				},
			)
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, "epp-config"),
				makeConfigMap("epp-config", testNamespace, map[string]string{"config.yaml": configJSON}),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()

			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disaggregated).To(BeTrue())
			Expect(result.LabelConfig.LabelKey).To(Equal("custom.io/role"))
			Expect(result.LabelConfig.PrefillValues).To(Equal([]string{"pf"}))
			Expect(result.LabelConfig.DecodeValues).To(Equal([]string{"dc", "mixed"}))
			Expect(result.LabelConfig.BothValues).To(BeNil())
		})

		It("should classify overlapping values as BothValues", func() {
			// "all" appears in both prefill and decode profiles → should be BothValues
			prefillParams, _ := json.Marshal(byLabelParams{Label: "custom.io/role", ValidValues: []string{"pf", "all"}})
			decodeParams, _ := json.Marshal(byLabelParams{Label: "custom.io/role", ValidValues: []string{"dc", "all"}})

			configJSON := buildConfigJSON(
				[]epconfigv1alpha1.PluginSpec{
					{Name: "pf-filter", Type: PluginTypeByLabel, Parameters: prefillParams},
					{Name: "dc-filter", Type: PluginTypeByLabel, Parameters: decodeParams},
				},
				[]epconfigv1alpha1.SchedulingProfile{
					{Name: "prefill", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "pf-filter"}}},
					{Name: "decode", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "dc-filter"}}},
				},
			)
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, "epp-config"),
				makeConfigMap("epp-config", testNamespace, map[string]string{"config.yaml": configJSON}),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()

			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disaggregated).To(BeTrue())
			Expect(result.LabelConfig.LabelKey).To(Equal("custom.io/role"))
			Expect(result.LabelConfig.PrefillValues).To(Equal([]string{"pf"}))
			Expect(result.LabelConfig.DecodeValues).To(Equal([]string{"dc"}))
			Expect(result.LabelConfig.BothValues).To(Equal([]string{"all"}))
		})
	})

	Context("fallback scenarios (disaggregated=false, no error)", func() {
		It("should return disaggregated=false with nil error when pool is nil", func() {
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
			Expect(result.LabelConfig).To(Equal(DefaultPDRoleLabelConfig()))
		})

		It("should return disaggregated=false with nil error when pool has nil EndpointPicker", func() {
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).Build()
			poolNoEPP := &poolutil.EndpointPool{Name: "no-epp", Namespace: testNamespace}
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, poolNoEPP)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
			Expect(result.LabelConfig).To(Equal(DefaultPDRoleLabelConfig()))
		})

		It("should return disaggregated=false with nil error when config has no P/D plugins", func() {
			configJSON := buildConfigJSON(
				[]epconfigv1alpha1.PluginSpec{{Name: "scorer", Type: "kv-cache-scorer"}},
				[]epconfigv1alpha1.SchedulingProfile{
					{Name: "default", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "scorer"}}},
				},
			)
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, "epp-config"),
				makeConfigMap("epp-config", testNamespace, map[string]string{"config.yaml": configJSON}),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})
	})

	Context("error scenarios", func() {
		It("should return error when service not found", func() {
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).Build()
			poolMissing := makePool("missing-pool", testNamespace, "nonexistent")
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, poolMissing)
			Expect(err).To(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})

		It("should return error when no deployment matches service selector", func() {
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)
			Expect(err).To(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})

		It("should return error when no ConfigMap is mounted", func() {
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, ""),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)
			Expect(err).To(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})

		It("should return error when ConfigMap has invalid data", func() {
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, "epp-config"),
				makeConfigMap("epp-config", testNamespace, map[string]string{"config.yaml": "not valid yaml {{{"}),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)
			Expect(err).To(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})

		It("should return error when config has no plugins or profiles", func() {
			objects := []client.Object{
				makeService("epp-svc", testNamespace, eppLabels),
				makeEPPDeployment("epp", testNamespace, eppLabels, "epp-config"),
				makeConfigMap("epp-config", testNamespace, map[string]string{"config.yaml": `{}`}),
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objects...).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)
			Expect(err).To(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})

		It("should return error when service has no selector", func() {
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "epp-svc", Namespace: testNamespace},
				Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "metrics", Port: 9090}}},
			}
			k8sClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(svc).Build()
			result, err := DiscoverPDRoleLabelConfig(ctx, k8sClient, pool)
			Expect(err).To(HaveOccurred())
			Expect(result.Disaggregated).To(BeFalse())
		})
	})
})

var _ = Describe("parseEndpointPickerConfig", func() {
	It("should parse valid JSON", func() {
		data := `{"plugins":[{"name":"p","type":"prefill-filter"}],"schedulingProfiles":[{"name":"prefill","plugins":[{"pluginRef":"p"}]}]}`
		epc, err := parseEndpointPickerConfig([]byte(data))
		Expect(err).NotTo(HaveOccurred())
		Expect(epc).NotTo(BeNil())
		Expect(epc.Plugins).To(HaveLen(1))
	})

	It("should parse valid YAML", func() {
		data := `
plugins:
  - name: p
    type: prefill-filter
schedulingProfiles:
  - name: prefill
    plugins:
      - pluginRef: p
`
		epc, err := parseEndpointPickerConfig([]byte(data))
		Expect(err).NotTo(HaveOccurred())
		Expect(epc).NotTo(BeNil())
		Expect(epc.Plugins).To(HaveLen(1))
	})

	It("should reject invalid data", func() {
		_, err := parseEndpointPickerConfig([]byte("not valid {{"))
		Expect(err).To(HaveOccurred())
	})

	It("should reject empty plugins and profiles", func() {
		_, err := parseEndpointPickerConfig([]byte(`{}`))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("extractPDRoleLabelConfig", func() {
	Context("with dedicated filter types", func() {
		It("should return default label config", func() {
			epc := &epconfigv1alpha1.EndpointPickerConfig{
				Plugins: []epconfigv1alpha1.PluginSpec{
					{Name: "pf", Type: PluginTypePrefillFilter},
					{Name: "df", Type: PluginTypeDecodeFilter},
				},
				SchedulingProfiles: []epconfigv1alpha1.SchedulingProfile{
					{Name: "prefill", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "pf"}}},
				},
			}
			config, found := extractPDRoleLabelConfig(epc)
			Expect(found).To(BeTrue())
			Expect(config.LabelKey).To(Equal(DefaultRoleLabel))
		})
	})

	Context("with by-label plugins", func() {
		It("should extract custom label config from scheduling profiles", func() {
			params, _ := json.Marshal(byLabelParams{Label: "custom/role", ValidValues: []string{"serve-prefill"}})
			decodeParams, _ := json.Marshal(byLabelParams{Label: "custom/role", ValidValues: []string{"serve-decode"}})

			epc := &epconfigv1alpha1.EndpointPickerConfig{
				Plugins: []epconfigv1alpha1.PluginSpec{
					{Name: "pf-label", Type: PluginTypeByLabel, Parameters: params},
					{Name: "dc-label", Type: PluginTypeByLabel, Parameters: decodeParams},
				},
				SchedulingProfiles: []epconfigv1alpha1.SchedulingProfile{
					{Name: "prefill-profile", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "pf-label"}}},
					{Name: "decode-profile", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "dc-label"}}},
				},
			}
			config, found := extractPDRoleLabelConfig(epc)
			Expect(found).To(BeTrue())
			Expect(config.LabelKey).To(Equal("custom/role"))
			Expect(config.PrefillValues).To(Equal([]string{"serve-prefill"}))
			Expect(config.DecodeValues).To(Equal([]string{"serve-decode"}))
			Expect(config.BothValues).To(BeNil())
		})

		It("should classify overlapping values as BothValues", func() {
			params, _ := json.Marshal(byLabelParams{Label: "custom/role", ValidValues: []string{"pf", "shared"}})
			decodeParams, _ := json.Marshal(byLabelParams{Label: "custom/role", ValidValues: []string{"dc", "shared"}})

			epc := &epconfigv1alpha1.EndpointPickerConfig{
				Plugins: []epconfigv1alpha1.PluginSpec{
					{Name: "pf-label", Type: PluginTypeByLabel, Parameters: params},
					{Name: "dc-label", Type: PluginTypeByLabel, Parameters: decodeParams},
				},
				SchedulingProfiles: []epconfigv1alpha1.SchedulingProfile{
					{Name: "prefill-profile", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "pf-label"}}},
					{Name: "decode-profile", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "dc-label"}}},
				},
			}
			config, found := extractPDRoleLabelConfig(epc)
			Expect(found).To(BeTrue())
			Expect(config.LabelKey).To(Equal("custom/role"))
			Expect(config.PrefillValues).To(Equal([]string{"pf"}))
			Expect(config.DecodeValues).To(Equal([]string{"dc"}))
			Expect(config.BothValues).To(Equal([]string{"shared"}))
		})
	})

	Context("with no P/D plugins", func() {
		It("should return not found", func() {
			epc := &epconfigv1alpha1.EndpointPickerConfig{
				Plugins: []epconfigv1alpha1.PluginSpec{{Name: "scorer", Type: "kv-cache-scorer"}},
				SchedulingProfiles: []epconfigv1alpha1.SchedulingProfile{
					{Name: "default", Plugins: []epconfigv1alpha1.SchedulingPlugin{{PluginRef: "scorer"}}},
				},
			}
			_, found := extractPDRoleLabelConfig(epc)
			Expect(found).To(BeFalse())
		})
	})
})

var _ = Describe("isConfigKey", func() {
	DescribeTable("should correctly identify config keys",
		func(key string, expected bool) {
			Expect(isConfigKey(key)).To(Equal(expected))
		},
		Entry("config.yaml", "config.yaml", true),
		Entry("config.yml", "config.yml", true),
		Entry("config.json", "config.json", true),
		Entry("config", "config", true),
		Entry("epp-config", "epp-config", true),
		Entry("endpointpickerconfig", "endpointpickerconfig", true),
		Entry("random-key", "random-key", false),
		Entry("data.txt", "data.txt", false),
		Entry("CONFIG.YAML", "CONFIG.YAML", true),
	)
})
