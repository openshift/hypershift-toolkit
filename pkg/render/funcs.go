package render

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"

	"github.com/openshift/hypershift-toolkit/pkg/api"
)

func imageFunc(images map[string]string) func(string) string {
	return func(imageName string) string {
		return images[imageName]
	}
}

func pkiFunc(pkiDir string) func(string) string {
	return func(fileName string) string {
		file := filepath.Join(pkiDir, fileName)
		if _, err := os.Stat(file); err != nil {
			panic(err.Error())
		}
		b, err := ioutil.ReadFile(file)
		if err != nil {
			panic(err.Error())
		}
		return base64.StdEncoding.EncodeToString(b)
	}
}

func base64Func(params *api.ClusterParams, rc *renderContext) func(string) string {
	return func(fileName string) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		return base64.StdEncoding.EncodeToString([]byte(result))
	}
}

func cidrAddress(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	return ip.String()
}

func cidrMask(cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	m := ipNet.Mask
	if len(m) != 4 {
		panic("Expecting a 4-byte mask")
	}
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}
