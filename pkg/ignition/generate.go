package ignition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	// gocidr "github.com/apparentlymart/go-cidr/cidr"
	igntypes "github.com/coreos/ignition/config/v2_2/types"
	"github.com/vincent-petithory/dataurl"

	"github.com/openshift/hypershift-toolkit/pkg/api"
	"github.com/openshift/hypershift-toolkit/pkg/assets"
	"github.com/openshift/hypershift-toolkit/pkg/release"
)

func GenerateIgnition(params *api.ClusterParams, sshPublicKey []byte, pullSecretFile, pkiDir, outputDir string) error {

	cfg := &igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: igntypes.MaxVersion.String(),
		},
	}

	cfg.Passwd.Users = append(
		cfg.Passwd.Users,
		igntypes.PasswdUser{Name: "core", SSHAuthorizedKeys: []igntypes.SSHAuthorizedKey{igntypes.SSHAuthorizedKey(sshPublicKey)}},
	)

	images, err := release.GetReleaseImagePullRefs(params.ReleaseImage, params.OriginReleasePrefix, pullSecretFile)
	if err != nil {
		return err
	}

	if err := addFile(cfg, filepath.Join(pkiDir, "kubelet-bootstrap.kubeconfig"), "/etc/kubernetes/kubeconfig", 0444); err != nil {
		return err
	}
	if err := addFile(cfg, filepath.Join(pkiDir, "root-ca.crt"), "/etc/kubernetes/ca.crt", 0644); err != nil {
		return err
	}
	if err := addFile(cfg, pullSecretFile, "/var/lib/kubelet/config.json", 0444); err != nil {
		return err
	}

	if err := addAssetFiles(cfg, params, "ignition/files", "ignition/files", images); err != nil {
		return err
	}

	if err := addUnits(cfg, "ignition/units"); err != nil {
		return err
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal Ignition config: %v", err)
	}

	return ioutil.WriteFile(filepath.Join(outputDir, "bootstrap.ign"), data, 0644)
}

func addAssetFiles(cfg *igntypes.Config, params *api.ClusterParams, prefix, assetPath string, images map[string]string) error {
	funcs := template.FuncMap{
		"cidrPrefix":  cidrPrefix,
		"imageFor":    imageFunc(images),
		"apiServerIP": APIServerIP,
	}
	data, err := assets.Asset(assetPath)
	if err == nil {
		destPath := path.Join("/", strings.TrimPrefix(assetPath, prefix))
		if strings.HasSuffix(path.Base(assetPath), ".template") {
			out := &bytes.Buffer{}
			t := template.Must(template.New("template").Funcs(funcs).Parse(string(data)))
			err := t.Execute(out, params)
			if err != nil {
				return err
			}
			data = out.Bytes()
			destPath = strings.TrimSuffix(destPath, ".template")
		}
		isBin := path.Base(path.Dir(destPath)) == "bin"
		if isBin {
			addFileBytes(cfg, data, destPath, 0755)
		} else {
			addFileBytes(cfg, data, destPath, 0644)
		}
		return nil
	}
	files, err := assets.AssetDir(assetPath)
	if err != nil {
		return fmt.Errorf("cannot get asset directory listing for %s: %v", assetPath, err)
	}
	for _, f := range files {
		if err := addAssetFiles(cfg, params, prefix, path.Join(assetPath, f), images); err != nil {
			return err
		}
	}
	return nil
}

func addUnits(cfg *igntypes.Config, filePath string) error {
	files, err := assets.AssetDir(filePath)
	if err != nil {
		return fmt.Errorf("cannot get asset directory listing for units path %s: %v", filePath, err)
	}
	for _, f := range files {
		data, err := assets.Asset(path.Join(filePath, f))
		if err != nil {
			return fmt.Errorf("cannot read unit file %s: %v", f, err)
		}
		name := path.Base(f)

		unit := igntypes.Unit{
			Name:     name,
			Contents: string(data),
			Enabled:  func() *bool { t := true; return &t }(),
		}
		cfg.Systemd.Units = append(cfg.Systemd.Units, unit)
	}
	return nil

}

func addFileBytes(cfg *igntypes.Config, data []byte, destPath string, mode int) {
	file := fileFromBytes(destPath, "root", mode, data)
	cfg.Storage.Files = append(cfg.Storage.Files, file)
}

func addFile(cfg *igntypes.Config, filePath string, destPath string, mode int) error {
	fileBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %v", filePath, err)
	}
	addFileBytes(cfg, fileBytes, destPath, mode)
	return nil
}

// FileFromBytes creates an ignition-config file with the given contents.
func fileFromBytes(path string, username string, mode int, contents []byte) igntypes.File {
	return igntypes.File{
		Node: igntypes.Node{
			Filesystem: "root",
			Path:       path,
			User: &igntypes.NodeUser{
				Name: username,
			},
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Mode: &mode,
			Contents: igntypes.FileContents{
				Source: dataurl.EncodeBytes(contents),
			},
		},
	}
}

func cidrPrefix(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	parts := strings.Split(ip.String(), ".")
	result := fmt.Sprintf("%s.%s", parts[0], parts[1])
	return result
}

func APIServerIP(serviceCIDR string) string {
	// For now, simply return a fixed IP in the 169.168.0.0/16 network
	return "192.168.255.254"
}

func imageFunc(images map[string]string) func(string) string {
	return func(imageName string) string {
		return images[imageName]
	}
}
