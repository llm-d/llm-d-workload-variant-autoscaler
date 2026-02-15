package pdrole

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeDeployment(name string, labels map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
			},
		},
	}
}

var _ = Describe("GetDeploymentPDRole", func() {
	var defaultConfig PDRoleLabelConfig

	BeforeEach(func() {
		defaultConfig = DefaultPDRoleLabelConfig()
	})

	Context("with nil deployment", func() {
		It("should return RoleUnknown", func() {
			Expect(GetDeploymentPDRole(nil, defaultConfig)).To(Equal(RoleUnknown))
		})
	})

	Context("with label-based detection", func() {
		It("should detect prefill from label", func() {
			deploy := makeDeployment("vllm-llama", map[string]string{DefaultRoleLabel: "prefill"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RolePrefill))
		})

		It("should detect decode from label", func() {
			deploy := makeDeployment("vllm-llama", map[string]string{DefaultRoleLabel: "decode"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleDecode))
		})

		It("should detect both from label", func() {
			deploy := makeDeployment("vllm-llama", map[string]string{DefaultRoleLabel: "both"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleBoth))
		})

		It("should return unknown for unrecognized label value", func() {
			deploy := makeDeployment("vllm-llama", map[string]string{DefaultRoleLabel: "invalid"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleUnknown))
		})
	})

	Context("without P/D label", func() {
		It("should return unknown even if name contains prefill", func() {
			deploy := makeDeployment("llama-prefill-a100", map[string]string{"app": "vllm"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleUnknown))
		})

		It("should return unknown even if name contains decode", func() {
			deploy := makeDeployment("llama-decode-h100", map[string]string{"app": "vllm"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleUnknown))
		})

		It("should return unknown with no labels at all", func() {
			deploy := makeDeployment("llama-prefill", nil)
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleUnknown))
		})

		It("should return unknown with generic name and no P/D label", func() {
			deploy := makeDeployment("vllm-llama", map[string]string{"app": "vllm"})
			Expect(GetDeploymentPDRole(deploy, defaultConfig)).To(Equal(RoleUnknown))
		})
	})

	Context("with custom label config", func() {
		It("should detect prefill with custom label key and values", func() {
			config := PDRoleLabelConfig{
				LabelKey:      "my.org/pd-role",
				PrefillValues: []string{"p"},
				DecodeValues:  []string{"d"},
				BothValues:    []string{"pd"},
			}
			deploy := makeDeployment("vllm-llama", map[string]string{"my.org/pd-role": "p"})
			Expect(GetDeploymentPDRole(deploy, config)).To(Equal(RolePrefill))
		})

		It("should detect decode with custom label key and values", func() {
			config := PDRoleLabelConfig{
				LabelKey:      "my.org/pd-role",
				PrefillValues: []string{"p"},
				DecodeValues:  []string{"d"},
				BothValues:    []string{"pd"},
			}
			deploy := makeDeployment("vllm-llama", map[string]string{"my.org/pd-role": "d"})
			Expect(GetDeploymentPDRole(deploy, config)).To(Equal(RoleDecode))
		})

		It("should detect both with custom label key and values", func() {
			config := PDRoleLabelConfig{
				LabelKey:      "my.org/pd-role",
				PrefillValues: []string{"p"},
				DecodeValues:  []string{"d"},
				BothValues:    []string{"pd"},
			}
			deploy := makeDeployment("vllm-llama", map[string]string{"my.org/pd-role": "pd"})
			Expect(GetDeploymentPDRole(deploy, config)).To(Equal(RoleBoth))
		})

		It("should return unknown when label key is empty", func() {
			config := PDRoleLabelConfig{LabelKey: ""}
			deploy := makeDeployment("vllm-llama", map[string]string{DefaultRoleLabel: "prefill"})
			Expect(GetDeploymentPDRole(deploy, config)).To(Equal(RoleUnknown))
		})
	})
})

var _ = Describe("DefaultPDRoleLabelConfig", func() {
	It("should return standard configuration", func() {
		config := DefaultPDRoleLabelConfig()
		Expect(config.LabelKey).To(Equal(DefaultRoleLabel))
		Expect(config.PrefillValues).To(Equal([]string{"prefill"}))
		Expect(config.DecodeValues).To(Equal([]string{"decode"}))
		Expect(config.BothValues).To(Equal([]string{"both"}))
	})
})
