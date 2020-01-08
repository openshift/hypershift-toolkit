package aws

import (
	"bytes"
	"io/ioutil"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	configapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/kubectl/pkg/cmd/apply"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

const (
	tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

type Applier struct {
	restConfig       *rest.Config
	factory          cmdutil.Factory
	defaultNamespace string
}

func NewApplier(cfg *rest.Config, namespace string) *Applier {
	return &Applier{
		restConfig:       cfg,
		defaultNamespace: namespace,
	}
}

func (a *Applier) ApplyFile(fileName string) error {
	factory, err := a.getFactory()
	if err != nil {
		return err
	}
	applyOptions, err := a.setupApplyCommand(factory, fileName, a.defaultNamespace)
	if err != nil {
		return err
	}
	return applyOptions.Run()
}

func (a *Applier) getFactory() (cmdutil.Factory, error) {
	if a.factory == nil {
		a.factory = cmdutil.NewFactory(&restConfigClientGetter{restConfig: a.restConfig, namespace: a.defaultNamespace})
	}
	return a.factory, nil
}

func (a *Applier) setupApplyCommand(f cmdutil.Factory, fileName, namespace string) (*apply.ApplyOptions, error) {
	o := apply.NewApplyOptions(genericclioptions.IOStreams{
		In:     &bytes.Buffer{},
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	})
	dynamicClient, err := dynamic.NewForConfig(a.restConfig)
	if err != nil {
		return nil, err
	}
	o.DeleteOptions = o.DeleteFlags.ToOptions(dynamicClient, o.IOStreams)
	o.OpenAPISchema, _ = f.OpenAPISchema()
	o.Validator, err = f.Validator(false)
	if err != nil {
		return nil, err
	}
	o.Builder = f.NewBuilder()
	o.Mapper, err = f.ToRESTMapper()
	if err != nil {
		return nil, err
	}

	o.DynamicClient = dynamicClient
	o.Namespace, _, err = f.ToRawKubeConfigLoader().Namespace()
	o.EnforceNamespace = false
	if err != nil {
		return nil, err
	}
	if len(namespace) > 0 {
		o.Namespace = namespace
	}
	o.DeleteOptions.FilenameOptions.Filenames = []string{fileName}
	o.ToPrinter = func(string) (printers.ResourcePrinter, error) { return o.PrintFlags.ToPrinter() }
	return o, nil
}

type restConfigClientGetter struct {
	restConfig *rest.Config
	namespace  string
}

// ToRESTConfig returns restconfig
func (r *restConfigClientGetter) ToRESTConfig() (*rest.Config, error) {
	return r.restConfig, nil
}

// ToDiscoveryClient returns discovery client
func (r *restConfigClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config := rest.CopyConfig(r.restConfig)
	cacheDir, err := ioutil.TempDir("", "")
	if err != nil {
		return nil, err
	}
	clientDir, err := ioutil.TempDir("", "")
	if err != nil {
		return nil, err
	}
	return disk.NewCachedDiscoveryClientForConfig(config, cacheDir, clientDir, 10*time.Minute)
}

// ToRESTMapper returns a restmapper
func (r *restConfigClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := r.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
	return expander, nil
}

// ToRawKubeConfigLoader return kubeconfig loader as-is
func (r *restConfigClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	cfg := GenerateClientConfigFromRESTConfig("default", r.restConfig)
	overrides := &clientcmd.ConfigOverrides{}
	if len(r.namespace) > 0 {
		overrides.Context.Namespace = r.namespace
	}
	return clientcmd.NewNonInteractiveClientConfig(*cfg, "", overrides, nil)
}

// GenerateClientConfigFromRESTConfig generates a new kubeconfig using a given rest.Config.
// The rest.Config may come from in-cluster config (as in a pod) or an existing kubeconfig.
func GenerateClientConfigFromRESTConfig(name string, restConfig *rest.Config) *configapi.Config {
	cfg := &configapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		Clusters:       map[string]*configapi.Cluster{},
		AuthInfos:      map[string]*configapi.AuthInfo{},
		Contexts:       map[string]*configapi.Context{},
		CurrentContext: name,
	}

	cluster := &configapi.Cluster{
		Server:                   restConfig.Host,
		InsecureSkipTLSVerify:    restConfig.Insecure,
		CertificateAuthority:     restConfig.CAFile,
		CertificateAuthorityData: restConfig.CAData,
	}

	authInfo := &configapi.AuthInfo{
		ClientCertificate:     restConfig.CertFile,
		ClientCertificateData: restConfig.CertData,
		ClientKey:             restConfig.KeyFile,
		ClientKeyData:         restConfig.KeyData,
		Token:                 restConfig.BearerToken,
		Username:              restConfig.Username,
		Password:              restConfig.Password,
	}

	if restConfig.WrapTransport != nil && len(restConfig.BearerToken) == 0 {
		token, err := ioutil.ReadFile(tokenFile)
		if err != nil {
			return nil
		} else {
			authInfo.Token = string(token)
		}
	}

	context := &configapi.Context{
		Cluster:  name,
		AuthInfo: name,
	}

	cfg.Clusters[name] = cluster
	cfg.AuthInfos[name] = authInfo
	cfg.Contexts[name] = context

	return cfg
}
