// Copyright (C) 2024 Akave
// See LICENSE for copying information.

package encryption_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"akave.ai/akavesdk/private/encryption"
)

func TestEncryption(t *testing.T) {
	type TestData struct {
		name string
		key  string
		data string
		info []byte
	}

	testData := []TestData{
		{
			name: "without info",
			key:  "foo",
			data: "big brown fox jumps over the lazy dog",
			info: nil,
		},
		{
			name: "with info",
			key:  "foo",
			data: "big brown fox jumps over the lazy dog",
			info: []byte("info"),
		},
	}

	for _, td := range testData {
		t.Run(td.name, func(t *testing.T) {
			t.Logf("%s len(data) %d", td.name, len(td.data))

			encrypted, err := encryption.Encrypt([]byte(td.key), []byte(td.data), td.info)
			require.NoError(t, err)

			t.Logf("%s encrypted data: %s", td.name, base64.StdEncoding.EncodeToString(encrypted))
			t.Logf("%s encrypted len(data): %d", td.name, len(encrypted))

			decrypted, err := encryption.Decrypt([]byte(td.key), encrypted, td.info)
			require.NoError(t, err)

			t.Logf("%s descrypted data: %s", td.name, string(decrypted))

			require.Equal(t, td.data, string(decrypted))
		})
	}
}
