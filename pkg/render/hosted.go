package render

import (
	"bytes"
	"encoding/base64"
	"io/ioutil"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pkg/errors"

	"github.com/openshift/hypershift-toolkit/pkg/api"
	assets "github.com/openshift/hypershift-toolkit/pkg/assets/v420_assets"
	"github.com/openshift/hypershift-toolkit/pkg/release"
)

type renderContext struct {
	outputDir         string
	params            *api.ClusterParams
	funcs             template.FuncMap
	manifestFiles     []string
	manifests         map[string]string
	userManifestFiles []string
	userManifests     map[string]string
}

func RenderClusterManifests(params *api.ClusterParams, pullSecretFile, outputDir, pkiDir string) error {
	images, err := release.GetReleaseImagePullRefs(params.ReleaseImage, pullSecretFile)
	if err != nil {
		return err
	}
	renderContext := &renderContext{
		params:        params,
		outputDir:     outputDir,
		manifests:     make(map[string]string),
		userManifests: make(map[string]string),
	}
	renderContext.funcs = template.FuncMap{
		"imageFor": imageFunc(images),
		"pki":      pkiFunc(pkiDir),
		"base64":   base64Func(params, renderContext),
		"address":  cidrAddress,
		"mask":     cidrMask,
	}
	renderContext.setupManifests()
	return renderContext.renderManifests()
}

func (c *renderContext) renderManifests() error {
	for _, f := range c.manifestFiles {
		outputFile := filepath.Join(c.outputDir, path.Base(f))
		content, err := c.substituteParams(c.params, f)
		if err != nil {
			return errors.Wrapf(err, "cannot render %s", f)
		}
		ioutil.WriteFile(outputFile, []byte(content), 0644)
	}

	for name, content := range c.manifests {
		outputFile := filepath.Join(c.outputDir, name)
		ioutil.WriteFile(outputFile, []byte(content), 0644)
	}

	return nil
}

func (c *renderContext) addManifestFiles(name ...string) {
	c.manifestFiles = append(c.manifestFiles, name...)
}

func (c *renderContext) addManifest(name, content string) {
	c.manifests[name] = content
}

func (c *renderContext) addUserManifestFiles(name ...string) {
	c.userManifestFiles = append(c.userManifestFiles, name...)
}

func (c *renderContext) addUserManifest(name, content string) {
	c.userManifests[name] = content
}

func (c *renderContext) setupManifests() {
	c.etcd()
	c.kubeAPIServer()
	c.kubeControllerManager()
	c.kubeScheduler()
	c.clusterBootstrap()
	c.openshiftAPIServer()
	c.openshiftControllerManager()
	c.openVPN()
	c.clusterVersionOperator()
	c.autoApprover()
	c.caOperator()
	c.userManifestsBootstrapper()
}

func (c *renderContext) etcd() {
	c.addManifestFiles(
		"etcd/etcd-cluster-crd.yaml",
		"etcd/etcd-cluster.yaml",
		"etcd/etcd-operator-cluster-role-binding.yaml",
		"etcd/etcd-operator-cluster-role.yaml",
		"etcd/etcd-operator.yaml",
	)

	for _, secret := range []string{"etcd-client", "server", "peer"} {
		file := secret
		if file != "etcd-client" {
			file = "etcd-" + secret
		}
		params := map[string]string{
			"secret": secret,
			"file":   file,
		}
		content, err := c.substituteParams(params, "etcd/etcd-secret-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		c.addManifest(file+"-tls-secret.yaml", content)
	}
}

func (c *renderContext) kubeAPIServer() {
	c.addManifestFiles(
		"kube-apiserver/kube-apiserver-deployment.yaml",
		"kube-apiserver/kube-apiserver-service.yaml",
		"kube-apiserver/kube-apiserver-secret.yaml",
		"kube-apiserver/openvpn-client-secret.yaml",
	)
}

func (c *renderContext) kubeControllerManager() {
	c.addManifestFiles(
		"kube-controller-manager/kube-controller-manager-deployment.yaml",
		"kube-controller-manager/kube-controller-manager-secret.yaml",
	)
}

func (c *renderContext) kubeScheduler() {
	c.addManifestFiles(
		"kube-scheduler/kube-scheduler-deployment.yaml",
		"kube-scheduler/kube-scheduler-secret.yaml",
	)
}

func (c *renderContext) clusterBootstrap() {
	manifests, err := assets.AssetDir("cluster-bootstrap")
	if err != nil {
		panic(err.Error())
	}
	for _, m := range manifests {
		c.addUserManifestFiles("cluster-bootstrap/" + m)
	}
}

func (c *renderContext) openshiftAPIServer() {
	c.addManifestFiles(
		"openshift-apiserver/openshift-apiserver-deployment.yaml",
		"openshift-apiserver/openshift-apiserver-service.yaml",
		"openshift-apiserver/openshift-apiserver-secret.yaml",
	)
	c.addUserManifestFiles(
		"openshift-apiserver/openshift-apiserver-user-service.yaml",
		"openshift-apiserver/openshift-apiserver-user-endpoint.yaml",
	)
	apiServices := &bytes.Buffer{}
	for _, apiService := range []string{
		"v1.apps.openshift.io",
		"v1.authorization.openshift.io",
		"v1.build.openshift.io",
		"v1.image.openshift.io",
		"v1.oauth.openshift.io",
		"v1.project.openshift.io",
		"v1.quota.openshift.io",
		"v1.route.openshift.io",
		"v1.security.openshift.io",
		"v1.template.openshift.io",
		"v1.user.openshift.io"} {

		params := map[string]string{
			"APIService":      apiService,
			"APIServiceGroup": trimFirstSegment(apiService),
		}
		entry, err := c.substituteParams(params, "openshift-apiserver/service-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		apiServices.WriteString(entry)
	}
	c.addUserManifest("openshift-apiserver-apiservices.yaml", apiServices.String())
}

func (c *renderContext) openshiftControllerManager() {
	c.addManifestFiles(
		"openshift-controller-manager/openshift-controller-manager-deployment.yaml",
		"openshift-controller-manager/openshift-controller-manager-secret.yaml",
	)
	c.addUserManifestFiles(
		"openshift-controller-manager/00-openshift-controller-manager-namespace.yaml",
		"openshift-controller-manager/openshift-controller-manager-service-ca.yaml",
	)
}

func (c *renderContext) openVPN() {
	c.addManifestFiles(
		"openvpn/openvpn-server-secret.yaml",
		"openvpn/openvpn-ccd-secret.yaml",
		"openvpn/openvpn-server-deployment.yaml",
		"openvpn/openvpn-server-service.yaml",
		"openvpn/openvpn-client-secret.yaml",
	)
	c.addUserManifestFiles(
		"openvpn/openvpn-client-deployment.yaml",
	)
}

func (c *renderContext) clusterVersionOperator() {
	c.addManifestFiles(
		"cluster-version-operator/cluster-version-operator-secret.yaml",
		"cluster-version-operator/cluster-version-operator-deployment.yaml",
	)
	c.addUserManifestFiles(
		"cluster-version-operator/cluster-version-namespace.yaml",
	)
}

func (c *renderContext) autoApprover() {
	c.addManifestFiles(
		"auto-approver/auto-approver-deployment.yaml",
		"auto-approver/auto-approver-secret.yaml",
	)
}

func (c *renderContext) caOperator() {
	c.addManifestFiles(
		"ca-operator/ca-operator-deployment.yaml",
		"ca-operator/ca-operator-secret.yaml",
	)
}

func (c *renderContext) userManifestsBootstrapper() {
	c.addManifestFiles(
		"user-manifests-bootstrapper/user-manifests-bootstrapper-pod.yaml",
		"user-manifests-bootstrapper/user-manifests-bootstrapper-secret.yaml",
	)
	for _, file := range c.userManifestFiles {
		data, err := c.substituteParams(c.params, file)
		if err != nil {
			panic(err.Error())
		}
		name := path.Base(file)
		params := map[string]string{
			"data": base64.StdEncoding.EncodeToString([]byte(data)),
			"name": userConfigMapName(name),
		}
		manifest, err := c.substituteParams(params, "user-manifests-bootstrapper/user-manifest-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		c.addManifest("user-manifest-"+name, manifest)
	}

	for name, data := range c.userManifests {
		params := map[string]string{
			"data": base64.StdEncoding.EncodeToString([]byte(data)),
			"name": userConfigMapName(name),
		}
		manifest, err := c.substituteParams(params, "user-manifests-bootstrapper/user-manifest-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		c.addManifest("user-manifest-"+name, manifest)
	}
}

func (c *renderContext) substituteParams(data interface{}, fileName string) (string, error) {
	out := &bytes.Buffer{}
	asset := assets.MustAsset(fileName)
	t := template.Must(template.New("template").Funcs(c.funcs).Parse(string(asset)))
	err := t.Execute(out, data)
	if err != nil {
		panic(err.Error())
	}
	return out.String(), nil
}

func trimFirstSegment(s string) string {
	parts := strings.Split(s, ".")
	return strings.Join(parts[1:], ".")
}

func userConfigMapName(file string) string {
	parts := strings.Split(file, ".")
	return "user-manifest-" + strings.ReplaceAll(parts[0], "_", "-")
}
