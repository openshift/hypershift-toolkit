package release

import (
	"os"

	"github.com/pkg/errors"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/openshift/oc/pkg/cli/admin/release"
)

func GetReleaseImagePullRefs(image string, pullSecretFile string) (map[string]string, error) {
	streams := genericclioptions.IOStreams{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
	options := release.NewInfoOptions(streams)
	options.SecurityOptions.RegistryConfig = pullSecretFile
	info, err := options.LoadReleaseInfo(image, false)
	if err != nil {
		return nil, err
	}
	if info.References == nil {
		return nil, errors.New("release image does not contain image references")
	}
	result := make(map[string]string)
	for _, tag := range info.References.Spec.Tags {
		result[tag.Name] = tag.From.Name
	}
	return result, nil
}
