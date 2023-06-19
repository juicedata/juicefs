//go:build !nohdfs
// +build !nohdfs

// Copyright 2014 Colin Marc (colinmarc@gmail.com)
// borrowed from https://github.com/colinmarc/hdfs/blob/master/cmd/hdfs/kerberos.go

package object

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	krb "github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
)

func getKerberosClient() (*krb.Client, error) {
	configPath := os.Getenv("KRB5_CONFIG")
	if configPath == "" {
		configPath = "/etc/krb5.conf"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Try to authenticate with keytab file first.
	keytabPath := os.Getenv("KRB5KEYTAB")
	keytabBase64 := os.Getenv("KRB5KEYTAB_BASE64")
	if keytabBase64 != "" {
		decodedKeytab, err := base64.StdEncoding.DecodeString(keytabBase64)
		if err != nil {
			return nil, fmt.Errorf("error decoding Base64 encoded data %s", err)
		}
		decodedKeytabPath := filepath.Join(os.TempDir(), "decodedKeytab")
		decodedKeytabFile, err := os.Create(decodedKeytabPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s", decodedKeytabPath)
		}
		defer decodedKeytabFile.Close()
		// keytab file format uses network byte order
		err = binary.Write(decodedKeytabFile, binary.BigEndian, decodedKeytab)
		if err != nil {
			return nil, fmt.Errorf("failed to write %s", decodedKeytabPath)
		}
		keytabPath = decodedKeytabPath
	}
	principal := os.Getenv("KRB5PRINCIPAL")
	if keytabPath != "" && principal != "" {
		kt, err := keytab.Load(keytabPath)
		if err != nil {
			return nil, err
		}
		// e.g. KRB5PRINCIPAL="primary/instance@realm"
		sp := strings.Split(principal, "@")
		if len(sp) != 2 {
			return nil, fmt.Errorf("unusable kerberos principal: %s", principal)
		}
		username, realm := sp[0], sp[1]
		logger.Infof("username: %s, realm: %s", username, realm)
		client := krb.NewWithKeytab(username, realm, kt, cfg)
		return client, nil
	}

	// Determine the ccache location from the environment, falling back to the
	// default location.
	ccachePath := os.Getenv("KRB5CCNAME")
	if strings.Contains(ccachePath, ":") {
		if strings.HasPrefix(ccachePath, "FILE:") {
			ccachePath = strings.SplitN(ccachePath, ":", 2)[1]
		} else {
			return nil, fmt.Errorf("unusable ccache: %s", ccachePath)
		}
	} else if ccachePath == "" {
		u, err := user.Current()
		if err != nil {
			return nil, err
		}

		ccachePath = fmt.Sprintf("/tmp/krb5cc_%s", u.Uid)
	}

	ccache, err := credentials.LoadCCache(ccachePath)
	if err != nil {
		return nil, err
	}

	client, err := krb.NewFromCCache(ccache, cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}
