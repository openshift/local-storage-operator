module github.com/openshift/local-storage-operator

go 1.25.0

require (
	github.com/ghodss/yaml v1.0.0
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/mitchellh/go-homedir v1.1.0
	github.com/onsi/gomega v1.39.1
	github.com/openshift/api v0.0.0-20260320151444-324a1bcb9f55
	github.com/openshift/build-machinery-go v0.0.0-20250602125535-1b6d00b8c37c
	github.com/openshift/client-go v0.0.0-20260320040014-4b5fc2cdad98
	github.com/openshift/library-go v0.0.0-20260318142011-72bf34f474bc
	github.com/pborman/uuid v1.2.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.90.0
	github.com/prometheus/client_golang v1.23.2
	github.com/rogpeppe/go-internal v1.14.1
	github.com/sirupsen/logrus v1.9.4
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.11.1
	go.uber.org/zap v1.27.1
	golang.org/x/net v0.52.0
	golang.org/x/sys v0.42.0
	k8s.io/api v0.35.2
	k8s.io/apiextensions-apiserver v0.35.2
	k8s.io/apimachinery v0.35.2
	k8s.io/client-go v1.5.2
	k8s.io/component-helpers v0.35.2
	k8s.io/klog/v2 v2.140.0
	k8s.io/utils v0.0.0-20260319190234-28399d86e0b5
	sigs.k8s.io/controller-runtime v0.23.3
	sigs.k8s.io/sig-storage-local-static-provisioner v0.0.0-20250130044123-3e55e7a25121
	sigs.k8s.io/yaml v1.6.0 // indirect
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/evanphx/json-patch v5.8.1+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.22.5 // indirect
	github.com/go-openapi/jsonreference v0.21.5 // indirect
	github.com/go-openapi/swag v0.25.5 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kubernetes-csi/csi-proxy/client v1.3.0 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/term v0.41.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.5.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260319201613-d00831a3d3e7 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiserver v0.35.2 // indirect
	k8s.io/component-base v0.35.2 // indirect
	k8s.io/kube-openapi v0.0.0-20260319004828-5883c5ee87b9 // indirect
	k8s.io/kubernetes v1.35.2 // indirect
	k8s.io/mount-utils v0.35.2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/kube-storage-version-migrator v0.0.6-0.20230721195810-5c8923c5ff96 // indirect
	sigs.k8s.io/sig-storage-lib-external-provisioner/v6 v6.3.0 // indirect
)

require (
	github.com/aws/aws-sdk-go-v2 v1.41.4
	github.com/aws/aws-sdk-go-v2/config v1.32.12
	github.com/aws/aws-sdk-go-v2/credentials v1.19.12
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.296.0
)

require (
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.20 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.20 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.20 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.9 // indirect
	github.com/aws/smithy-go v1.24.2 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-openapi/swag/cmdutils v0.25.5 // indirect
	github.com/go-openapi/swag/conv v0.25.5 // indirect
	github.com/go-openapi/swag/fileutils v0.25.5 // indirect
	github.com/go-openapi/swag/jsonname v0.25.5 // indirect
	github.com/go-openapi/swag/jsonutils v0.25.5 // indirect
	github.com/go-openapi/swag/loading v0.25.5 // indirect
	github.com/go-openapi/swag/mangling v0.25.5 // indirect
	github.com/go-openapi/swag/netutils v0.25.5 // indirect
	github.com/go-openapi/swag/stringutils v0.25.5 // indirect
	github.com/go-openapi/swag/typeutils v0.25.5 // indirect
	github.com/go-openapi/swag/yamlutils v0.25.5 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/imdario/mergo v0.3.7 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opentelemetry.io/otel v1.42.0 // indirect
	go.opentelemetry.io/otel/trace v1.42.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	k8s.io/controller-manager v0.35.2 // indirect
	k8s.io/kube-aggregator v0.35.2 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.2 // indirect
)

replace (
	k8s.io/api => k8s.io/api v0.35.2
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.35.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.35.2
	k8s.io/apiserver => k8s.io/apiserver v0.35.2
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.35.2
	k8s.io/client-go => k8s.io/client-go v0.35.2
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.35.2
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.35.2
	k8s.io/code-generator => k8s.io/code-generator v0.35.2
	k8s.io/component-base => k8s.io/component-base v0.35.2
	k8s.io/component-helpers => k8s.io/component-helpers v0.35.2
	k8s.io/controller-manager => k8s.io/controller-manager v0.35.2
	k8s.io/cri-api => k8s.io/cri-api v0.35.2
	k8s.io/cri-client => k8s.io/cri-client v0.35.2
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.35.2
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.35.2
	k8s.io/endpointslice => k8s.io/endpointslice v0.35.2
	k8s.io/externaljwt => k8s.io/externaljwt v0.35.2
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.35.2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.35.2
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.35.2
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.35.2
	k8s.io/kubectl => k8s.io/kubectl v0.35.2
	k8s.io/kubelet => k8s.io/kubelet v0.35.2
	k8s.io/kubernetes => k8s.io/kubernetes v1.35.2
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.35.2
	k8s.io/metrics => k8s.io/metrics v0.35.2
	k8s.io/mount-utils => k8s.io/mount-utils v0.35.2
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.35.2
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.35.2
)

replace k8s.io/kms => k8s.io/kms v0.35.2

replace k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.35.2

replace k8s.io/sample-controller => k8s.io/sample-controller v0.35.2
