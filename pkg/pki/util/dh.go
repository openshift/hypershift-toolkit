package util

import (
	dhparam "github.com/Luzifer/go-dhparam"
)

const (
	bitSize = 2048
)

func GenerateDHParams() ([]byte, error) {
	dh, err := dhparam.Generate(bitSize, dhparam.GeneratorTwo, nil)
	if err != nil {
		return nil, err
	}
	pem, err := dh.ToPEM()
	if err != nil {
		return nil, err
	}
	return pem, nil
}
