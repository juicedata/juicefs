package api

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// ExportableName a name which is exportable as a value or name in Go code
func (a *API) ExportableName(name string) string {
	if name == "" {
		return name
	}

	failed := false

	// make sure the symbol is exportable
	name = strings.ToUpper(name[0:1]) + name[1:]

	// inflections are disabled, stop here.
	if a.NoInflections {
		return name
	}

	// fix common AWS<->Go bugaboos
	out := ""
	for _, part := range splitName(name) {
		if part == "" {
			continue
		}
		if part == strings.ToUpper(part) || part[0:1]+"s" == part {
			out += part
			continue
		}
		if v, ok := whitelistExportNames[part]; ok {
			if v != "" {
				out += v
			} else {
				out += part
			}
		} else {
			failed = true
			inflected := part
			for regexp, repl := range replacements {
				inflected = regexp.ReplaceAllString(inflected, repl)
			}
			a.unrecognizedNames[part] = inflected
		}
	}

	if failed {
		return name
	}
	return out
}

// splitName splits name into a slice of strings split by capitalization.
func splitName(name string) []string {
	out, buf := []string{}, ""

	for i, r := range name {
		l := string(r)

		// special check for EC2, MD5, DBi
		lower := strings.ToLower(buf)
		if _, err := strconv.Atoi(l); err == nil &&
			(lower == "ec" || lower == "md" || lower == "db") {
			buf += l
			continue
		}

		lastUpper := i-1 >= 0 && strings.ToUpper(name[i-1:i]) == name[i-1:i]
		curUpper := l == strings.ToUpper(l)
		nextUpper := i+2 > len(name) || strings.ToUpper(name[i+1:i+2]) == name[i+1:i+2]

		if (lastUpper != curUpper) || (nextUpper != curUpper && !nextUpper) {
			if len(buf) > 1 || curUpper {
				out = append(out, buf)
				buf = ""
			}
			buf += l
		} else {
			buf += l
		}
	}
	if len(buf) > 0 {
		out = append(out, buf)
	}
	return out
}

// Generate the map of white listed exported names as soon as the package is initialized.
var whitelistExportNames = func() map[string]string {
	list := map[string]string{}
	_, filename, _, _ := runtime.Caller(1)
	f, err := os.Open(filepath.Join(filepath.Dir(filename), "inflections.csv"))
	if err != nil {
		panic(err)
	}

	b, err := ioutil.ReadAll(f)
	if err != nil {
		panic(err)
	}

	str := string(b)
	for _, line := range strings.Split(str, "\n") {
		line = strings.Replace(line, "\r", "", -1)
		if strings.HasPrefix(line, ";") {
			continue
		}
		parts := regexp.MustCompile(`\s*:\s*`).Split(line, -1)
		if len(parts) > 1 {
			list[parts[0]] = parts[1]
		}
	}

	return list
}()

var replacements = map[*regexp.Regexp]string{
	regexp.MustCompile(`Acl`):          "ACL",
	regexp.MustCompile(`Adm([^i]|$)`):  "ADM$1",
	regexp.MustCompile(`Aes`):          "AES",
	regexp.MustCompile(`Api`):          "API",
	regexp.MustCompile(`Ami`):          "AMI",
	regexp.MustCompile(`Apns`):         "APNS",
	regexp.MustCompile(`Arn`):          "ARN",
	regexp.MustCompile(`Asn`):          "ASN",
	regexp.MustCompile(`Aws`):          "AWS",
	regexp.MustCompile(`Bcc([A-Z])`):   "BCC$1",
	regexp.MustCompile(`Bgp`):          "BGP",
	regexp.MustCompile(`Cc([A-Z])`):    "CC$1",
	regexp.MustCompile(`Cidr`):         "CIDR",
	regexp.MustCompile(`Cors`):         "CORS",
	regexp.MustCompile(`Csv`):          "CSV",
	regexp.MustCompile(`Cpu`):          "CPU",
	regexp.MustCompile(`Db`):           "DB",
	regexp.MustCompile(`Dhcp`):         "DHCP",
	regexp.MustCompile(`Dns`):          "DNS",
	regexp.MustCompile(`Ebs`):          "EBS",
	regexp.MustCompile(`Ec2`):          "EC2",
	regexp.MustCompile(`Eip`):          "EIP",
	regexp.MustCompile(`Gcm`):          "GCM",
	regexp.MustCompile(`Html`):         "HTML",
	regexp.MustCompile(`Https`):        "HTTPS",
	regexp.MustCompile(`Http([^s]|$)`): "HTTP$1",
	regexp.MustCompile(`Hsm`):          "HSM",
	regexp.MustCompile(`Hvm`):          "HVM",
	regexp.MustCompile(`Iam`):          "IAM",
	regexp.MustCompile(`Icmp`):         "ICMP",
	regexp.MustCompile(`Id$`):          "ID",
	regexp.MustCompile(`Id([A-Z])`):    "ID$1",
	regexp.MustCompile(`Idn`):          "IDN",
	regexp.MustCompile(`Ids$`):         "IDs",
	regexp.MustCompile(`Ids([A-Z])`):   "IDs$1",
	regexp.MustCompile(`Iops`):         "IOPS",
	regexp.MustCompile(`Ip`):           "IP",
	regexp.MustCompile(`Jar`):          "JAR",
	regexp.MustCompile(`Json`):         "JSON",
	regexp.MustCompile(`Jvm`):          "JVM",
	regexp.MustCompile(`Kms`):          "KMS",
	regexp.MustCompile(`Mac([^h]|$)`):  "MAC$1",
	regexp.MustCompile(`Md5`):          "MD5",
	regexp.MustCompile(`Mfa`):          "MFA",
	regexp.MustCompile(`Ok`):           "OK",
	regexp.MustCompile(`Os`):           "OS",
	regexp.MustCompile(`Php`):          "PHP",
	regexp.MustCompile(`Raid`):         "RAID",
	regexp.MustCompile(`Ramdisk`):      "RAMDisk",
	regexp.MustCompile(`Rds`):          "RDS",
	regexp.MustCompile(`Sni`):          "SNI",
	regexp.MustCompile(`Sns`):          "SNS",
	regexp.MustCompile(`Sriov`):        "SRIOV",
	regexp.MustCompile(`Ssh`):          "SSH",
	regexp.MustCompile(`Ssl`):          "SSL",
	regexp.MustCompile(`Svn`):          "SVN",
	regexp.MustCompile(`Tar([^g]|$)`):  "TAR$1",
	regexp.MustCompile(`Tde`):          "TDE",
	regexp.MustCompile(`Tcp`):          "TCP",
	regexp.MustCompile(`Tgz`):          "TGZ",
	regexp.MustCompile(`Tls`):          "TLS",
	regexp.MustCompile(`Uri`):          "URI",
	regexp.MustCompile(`Url`):          "URL",
	regexp.MustCompile(`Vgw`):          "VGW",
	regexp.MustCompile(`Vhd`):          "VHD",
	regexp.MustCompile(`Vip`):          "VIP",
	regexp.MustCompile(`Vlan`):         "VLAN",
	regexp.MustCompile(`Vm([^d]|$)`):   "VM$1",
	regexp.MustCompile(`Vmdk`):         "VMDK",
	regexp.MustCompile(`Vpc`):          "VPC",
	regexp.MustCompile(`Vpn`):          "VPN",
	regexp.MustCompile(`Xml`):          "XML",
}
