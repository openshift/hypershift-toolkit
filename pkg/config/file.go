package config

import (
	"io/ioutil"

	"github.com/ghodss/yaml"
	"github.com/google/uuid"

	"github.com/openshift/hypershift-toolkit/pkg/api"
)

func ReadFrom(fileName string) (*api.ClusterParams, error) {
	result := &api.ClusterParams{}
	b, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(b, result)
	if err != nil {
		return nil, err
	}
	setDefaults(result)
	return result, nil
}

func setDefaults(params *api.ClusterParams) {
	if len(params.ImageRegistryHTTPSecret) == 0 {
		params.ImageRegistryHTTPSecret = uuid.New().String()
	}
}
