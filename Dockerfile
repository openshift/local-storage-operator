FROM registry.svc.ci.openshift.org/openshift/release:golang-1.13 AS builder
WORKDIR /go/src/github.com/openshift/local-storage-operator
COPY . .
RUN make build-operator

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/local-storage-operator/_output/bin/local-storage-operator /usr/bin/
COPY manifests /manifests
ENTRYPOINT ["/usr/bin/local-storage-operator"]
LABEL com.redhat.delivery.appregistry=true
LABEL io.k8s.display-name="OpenShift local-storage-operator" \
      io.k8s.description="This is a component of OpenShift and manages local volumes." \
        maintainer="Hemant Kumar <hekumar@redhat.com>"
