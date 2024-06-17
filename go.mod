module github.com/openshift/local-storage-operator

go 1.22

toolchain go1.22.3

require (
	github.com/aws/aws-sdk-go v1.50.9
	github.com/ghodss/yaml v1.0.0
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0
	github.com/onsi/gomega v1.31.1
	github.com/openshift/api v0.0.0-20240202140003-8b34b9854c7f
	github.com/openshift/client-go v0.0.0-20240125160436-aa5df63097c4
	github.com/openshift/library-go v0.0.0-20240130085015-2ad786549f07
	github.com/openshift/must-gather v0.0.0-20240606205211-408ab8661885
	github.com/pborman/uuid v1.2.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.71.2
	github.com/prometheus/client_golang v1.18.0
	github.com/rogpeppe/go-internal v1.12.0
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.8.0
	github.com/stretchr/testify v1.8.4
	go.uber.org/zap v1.26.0
	golang.org/x/net v0.20.0
	golang.org/x/sys v0.16.0
	k8s.io/api v0.29.1
	k8s.io/apiextensions-apiserver v0.29.1
	k8s.io/apimachinery v0.29.1
	k8s.io/client-go v1.5.2
	k8s.io/component-helpers v0.29.1
	k8s.io/klog/v2 v2.120.1
	k8s.io/utils v0.0.0-20240102154912-e7106e64919e
	sigs.k8s.io/controller-runtime v0.17.0
	sigs.k8s.io/sig-storage-local-static-provisioner v2.5.0+incompatible
	sigs.k8s.io/yaml v1.4.0 // indirect
)

require (
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.11.2 // indirect
	github.com/evanphx/json-patch v5.8.1+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.0 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.20.2 // indirect
	github.com/go-openapi/jsonreference v0.20.4 // indirect
	github.com/go-openapi/swag v0.22.9 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kubernetes-csi/csi-proxy/client v1.1.3 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/miekg/dns v1.1.58 // indirect
	github.com/moby/sys/mountinfo v0.7.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.5.0 // indirect
	github.com/prometheus/common v0.46.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20240119083558-1b970713d09a // indirect
	golang.org/x/mod v0.14.0 // indirect
	golang.org/x/oauth2 v0.16.0 // indirect
	golang.org/x/term v0.16.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	golang.org/x/tools v0.17.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240125205218-1f4bbc51befe // indirect
	google.golang.org/grpc v1.61.0 // indirect
	google.golang.org/protobuf v1.32.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiserver v0.29.1 // indirect
	k8s.io/component-base v0.29.1 // indirect
	k8s.io/kube-openapi v0.0.0-20240126223410-2919ad4fcfec // indirect
	k8s.io/kubernetes v1.29.1 // indirect
	k8s.io/mount-utils v0.29.1 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/kube-storage-version-migrator v0.0.6-0.20230721195810-5c8923c5ff96 // indirect
	sigs.k8s.io/sig-storage-lib-external-provisioner/v6 v6.3.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
)

replace (
	k8s.io/api => k8s.io/api v0.28.2
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.28.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.28.2
	k8s.io/apiserver => k8s.io/apiserver v0.28.2
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.28.2
	k8s.io/client-go => k8s.io/client-go v0.28.2
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.28.2
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.28.2
	k8s.io/code-generator => k8s.io/code-generator v0.28.2
	k8s.io/component-base => k8s.io/component-base v0.28.2
	k8s.io/component-helpers => k8s.io/component-helpers v0.28.2
	k8s.io/controller-manager => k8s.io/controller-manager v0.28.2
	k8s.io/cri-api => k8s.io/cri-api v0.28.2
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.28.2
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.28.2
	k8s.io/endpointslice => k8s.io/endpointslice v0.28.2
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.28.2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.28.2
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.28.2
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.28.2
	k8s.io/kubectl => k8s.io/kubectl v0.28.2
	k8s.io/kubelet => k8s.io/kubelet v0.28.2
	k8s.io/kubernetes => k8s.io/kubernetes v1.28.2
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.28.2
	k8s.io/metrics => k8s.io/metrics v0.28.2
	k8s.io/mount-utils => k8s.io/mount-utils v0.28.2
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.28.2
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.28.2
)

replace sigs.k8s.io/sig-storage-local-static-provisioner => github.com/openshift/sig-storage-local-static-provisioner v0.0.0-20221121145404-891b3d12b1a9 //BUG: https://issues.redhat.com/browse/OCPBUGS-2450

replace github.com/openshift/must-gather => ../must-gather

replace github.com/dgrijalva/jwt-go => github.com/golang-jwt/jwt v3.2.1+incompatible
