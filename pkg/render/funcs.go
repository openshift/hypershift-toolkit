package render

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
)

func includeVPNFunc(includeVPN bool) func() bool {
	return func() bool {
		return includeVPN
	}
}

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

func includePKIFunc(pkiDir string) func(string, int) string {
	return func(fileName string, indent int) string {
		file := filepath.Join(pkiDir, fileName)
		if _, err := os.Stat(file); err != nil {
			panic(err.Error())
		}
		b, err := ioutil.ReadFile(file)
		if err != nil {
			panic(err.Error())
		}
		input := bytes.NewBuffer(b)
		output := &bytes.Buffer{}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			fmt.Fprintf(output, "%s%s\n", strings.Repeat(" ", indent), scanner.Text())
		}
		return output.String()
	}
}

func base64Func(params interface{}, rc *renderContext) func(string) string {
	return func(fileName string) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		return base64.StdEncoding.EncodeToString([]byte(result))
	}
}

func includeFileFunc(params interface{}, rc *renderContext) func(string, int) string {
	return func(fileName string, indent int) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		input := bytes.NewBufferString(result)
		output := &bytes.Buffer{}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			fmt.Fprintf(output, "%s%s\n", strings.Repeat(" ", indent), scanner.Text())
		}
		return output.String()
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
