package release

import (
	"github.com/pkg/errors"

	"fmt"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"os"
	"strings"

	"github.com/openshift/oc/pkg/cli/admin/release"
)

func GetReleaseImagePullRefs(image string, originReleasePrefix string, pullSecretFile string) (map[string]string, error) {
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

	var newImagePrefix string
	if !strings.Contains(image, originReleasePrefix) {
		newImagePrefix = strings.Replace(image, ":", "-", -1)
		fmt.Println(newImagePrefix)
	}
	result := make(map[string]string)
	for _, tag := range info.References.Spec.Tags {
		name := tag.From.Name
		if len(newImagePrefix) > 0 {
			name = fmt.Sprintf("%s@%s", newImagePrefix, strings.Split(tag.From.Name, "@")[1])
			fmt.Println("NAME", name)
		}

		result[tag.Name] = name
	}

	return result, nil
}
