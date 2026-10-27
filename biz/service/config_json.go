package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeConfigJSON preserves number tokens and accepts exactly one JSON value.
func DecodeConfigJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("multiple JSON values")
	}
	return nil
}

func jsonContent(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
