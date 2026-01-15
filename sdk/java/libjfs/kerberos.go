/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/service"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
)

const (
	defaultLife  = 3600 * 24 * 7
	defaultRenew = 3600 * 24
)

const (
	mechanismHadoop = "hadoop"
	mechanismMIT    = "mit"
)

var (
	namePattern     = regexp.MustCompile(`([^/@]+)(/([^/@]+))?(@([^/@]+))?`)
	paramPattern    = regexp.MustCompile(`[^$]*(\$\d)`)
	ruleParser      = regexp.MustCompile(`(\[(\d+):([^\]]+)\](\(([^\)]+)\))?(s/([^/]+)/([^/]*)/(g)?)?)/?(L)?`)
	noSimplePattern = regexp.MustCompile(`[/@]`)
)

type kRule struct {
	isDefault   bool
	comps       int
	format      string
	match       *regexp.Regexp
	fromPattern *regexp.Regexp
	toPattern   string
	repeat      bool
	lower       bool
}

func (r *kRule) String() string {
	if r.isDefault {
		return "DEFAULT"
	}
	s := fmt.Sprintf("RULE:[%d:%s]", r.comps, r.format)
	if r.match != nil {
		s += fmt.Sprintf("(%s)", r.match)
	}
	if r.fromPattern != nil {
		s += fmt.Sprintf("s/%s/%s/", r.fromPattern, r.toPattern)
		if r.repeat {
			s += "g"
		}
	}
	if r.lower {
		s += "/L"
	}
	return s
}

func (r *kRule) replaceParameters(params []string) string {
	return paramPattern.ReplaceAllStringFunc(r.format, func(s string) string {
		m := paramPattern.FindStringSubmatchIndex(s)
		i, _ := strconv.Atoi(s[m[2]+1:])
		if i >= len(params) {
			logger.Warnf("invalid param %s", s)
			return s
		}
		return s[:m[2]] + params[i]
	})
}

func (r *kRule) replaceSubs(base string) string {
	if r.fromPattern == nil {
		return base
	}
	if r.repeat {
		return r.fromPattern.ReplaceAllString(base, r.toPattern)
	}
	m := r.fromPattern.FindStringIndex(base)
	if m != nil {
		return base[:m[0]] + r.toPattern + base[m[1]:]
	}
	return base
}

func (r *kRule) apply(param []string, mechanism string, realm string) string {
	var result string
	if r.isDefault {
		if realm == "" || param[0] == realm {
			result = param[1]
		}
	} else if r.comps+1 == len(param) {
		base := r.replaceParameters(param)
		if r.match == nil || r.match.MatchString(base) {
			result = r.replaceSubs(base)
		}
	}
	if mechanism == mechanismHadoop && noSimplePattern.FindString(result) != "" {
		return ""
	}
	if r.lower {
		result = strings.ToLower(result)
	}
	return result
}

func parseRule(rule string) *kRule {
	rule = strings.TrimSpace(rule)
	if rule == "DEFAULT" {
		return &kRule{isDefault: true}
	}
	var r kRule
	m := ruleParser.FindStringSubmatch(rule)
	if m == nil {
		return nil
	}
	r.comps, _ = strconv.Atoi(m[2])
	r.format = m[3]
	var err error
	r.match, err = regexp.Compile(m[5])
	if err != nil {
		logger.Warnf("compile %s: %s", m[5], err)
		return nil
	}
	r.fromPattern, err = regexp.Compile(m[7])
	if err != nil {
		logger.Warnf("compile %s: %s", m[7], err)
		return nil
	}
	r.toPattern = m[8]
	r.repeat = m[9] == "g"
	r.lower = m[10] == "L"
	return &r
}

type kerberosRules struct {
	mechanism string
	realm     string
	rules     []*kRule
}

func newkerberosRules(mechanism string, realm string, rules []string) *kerberosRules {
	if mechanism == "" {
		mechanism = mechanismHadoop
	}
	var rs []*kRule
	for _, rule := range rules {
		rs = append(rs, parseRule(rule))
	}
	return &kerberosRules{mechanism, realm, rs}
}

func (r *kerberosRules) getShortName(full string) string {
	service, host, realm := parseFullName(full)
	var param []string
	if host == "" {
		if realm == "" {
			return service
		}
		param = []string{realm, service}
	} else {
		param = []string{realm, service, host}
	}
	if r.rules == nil {
		r.rules = append(r.rules, &kRule{isDefault: true})
	}
	for _, rule := range r.rules {
		short := rule.apply(param, r.mechanism, r.realm)
		if short != "" {
			return short
		}
	}
	if r.mechanism == mechanismHadoop {
		return ""
	}
	return full
}

func parseFullName(full string) (string, string, string) {
	m := namePattern.FindStringSubmatch(full)
	if m == nil || m[0] != full {
		return "", "", ""
	}
	return m[1], m[3], m[5]
}

type token struct {
	User     string
	Renewer  string
	Password string
	Issued   int64
	Expire   int64
}

type hostParam struct {
	allAllowed bool
	cidr       []*net.IPNet
	addrs      map[string]bool
}
type proxyParam struct {
	users  []string
	groups []string
	hosts  *hostParam
}

type volParams struct {
	m          meta.Meta
	keytab     []byte
	renew      int64
	life       int64
	superuser  string
	supergroup string
	rules      *kerberosRules
	proxies    map[string]*proxyParam
}

func (vol *volParams) parse(kind, key, value string) {
	if vol.rules == nil {
		vol.rules = newkerberosRules(mechanismHadoop, "", nil)
	}
	switch kind {
	case "keytab":
		kt, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			logger.Errorf("decode keytab failed: %s", err)
		} else {
			vol.keytab = kt
		}
	case "life":
		period, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			logger.Errorf("can not parse %s as int: %s", value, err)
		} else {
			vol.life = period
		}
	case "renew":
		period, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			logger.Errorf("can not parse %s as int: %s", value, err)
		} else {
			vol.renew = period
		}
	case "superuser":
		vol.superuser = value
	case "supergroup":
		vol.supergroup = value
	case "mechanism":
		value = strings.ToLower(value)
		if value != mechanismHadoop && value != mechanismMIT {
			logger.Errorf("invalid mechanism: %s", value)
		} else {
			vol.rules.mechanism = value
		}
	case "realm":
		vol.rules.realm = value
	case "rule":
		rule := parseRule(value)
		if rule != nil {
			vol.rules.rules = append(vol.rules.rules, rule)
		} else {
			logger.Errorf("invalid kerberos rule: %s", value)
		}
	default:
		split := strings.Split(key, ".")
		if len(split) < 4 || split[1] != "proxy" {
			logger.Warnf("invalid key: %s", key)
			return
		}
		user := split[2]
		proxy := vol.proxies[user]
		if proxy == nil {
			proxy = &proxyParam{hosts: &hostParam{}}
			vol.proxies[user] = proxy
		}
		switch kind {
		case "users":
			proxy.users = strings.Split(value, ",")
			for i := range proxy.users {
				proxy.users[i] = strings.TrimSpace(proxy.users[i])
			}
		case "groups":
			proxy.groups = strings.Split(value, ",")
			for i := range proxy.groups {
				proxy.groups[i] = strings.TrimSpace(proxy.groups[i])
			}
		case "hosts":
			m := proxy.hosts
			if strings.Contains(value, "*") {
				m.allAllowed = true
			} else {
				m.addrs = make(map[string]bool)
				for _, v := range strings.Split(value, ",") {
					if strings.Contains(v, "/") {
						// ip range
						_, ipnet, err := net.ParseCIDR(v)
						if err != nil {
							logger.Errorf("wrong ip range %s: %s", v, err)
							continue
						}
						m.cidr = append(m.cidr, ipnet)
					} else {
						m.addrs[v] = true
					}
				}
			}
		default:
			logger.Errorf("invalid key: %s", key)
		}
	}
}

func (vol *volParams) canProxy(realUser, user, group, ips, hostname string) bool {
	if realUser == "" || realUser == user {
		return true
	}
	if !vol.isUserGroupAllowed(realUser, user, group) {
		logger.Errorf("user: %s is not allowed to impersonate %s", realUser, user)
		return false
	}
	if !vol.isHostAllowed(realUser, ips, hostname) {
		logger.Errorf("user: %s is not allowed to impersonate %s on %s", realUser, user, hostname)
		return false
	}
	return true
}

func (vol *volParams) isUserGroupAllowed(realUser, user, groups string) bool {
	proxy := vol.proxies[realUser]
	if proxy == nil {
		return false
	}
	for _, u := range proxy.users {
		if u == "*" || u == user {
			return true
		}
	}
	for _, group := range strings.Split(groups, ",") {
		for _, ag := range proxy.groups {
			if ag == "*" || ag == group {
				return true
			}
		}
	}
	return false
}

func (vol *volParams) isHostAllowed(realUser, ips, hostname string) bool {
	proxy := vol.proxies[realUser]
	if proxy == nil {
		return false
	}
	m := proxy.hosts
	if m.allAllowed {
		return true
	}
	if m.addrs[hostname] {
		return true
	}
	for _, ip := range strings.Split(ips, ",") {
		if m.addrs[ip] {
			return true
		}
		for _, ipNet := range m.cidr {
			if net.ParseIP(ip) != nil && ipNet.Contains(net.ParseIP(ip)) {
				return true
			}
		}
	}
	return false
}

type kerberos struct {
	vols map[string]*volParams
	mu   sync.Mutex
}

func (k *kerberos) getVol(volname string) *volParams {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.vols[volname]
}

func (k *kerberos) auth(volname, user, realUser, group, ips, hostname string, reqBytes []byte) syscall.Errno {
	krb5Token := spnego.KRB5Token{}
	err := krb5Token.Unmarshal(reqBytes)
	req := krb5Token.APReq
	if err != nil {
		logger.Errorf("invalid AP_REQ: %s", err)
		return syscall.EINVAL
	}
	vol := k.getVol(volname)
	if vol == nil || vol.keytab == nil {
		logger.Errorf("server keytab for %s not setted", volname)
		return syscall.ENODATA
	}
	kt := new(keytab.Keytab)
	err = kt.Unmarshal(vol.keytab)
	if err != nil {
		logger.Errorf("unmarshal keytab: %s", err)
		return syscall.EINVAL
	}
	s := service.NewSettings(kt, service.DecodePAC(false))
	ok, creds, err := service.VerifyAPREQ(&req, s)
	if err != nil {
		logger.Errorf("verify: %s", err)
		return syscall.EINVAL
	} else if !ok {
		return syscall.EACCES
	}

	principal := fmt.Sprintf("%s@%s", creds.UserName(), creds.Realm())
	authedUser := vol.rules.getShortName(principal)
	if authedUser == "" {
		logger.Warnf("no rule for principal %s", principal)
		return syscall.EINVAL
	}

	if realUser == "" {
		if user == authedUser {
			return 0
		}
	} else {
		if realUser == authedUser && vol.canProxy(realUser, user, group, ips, hostname) {
			return 0
		}
	}
	logger.Warnf("auth failed, principal: %s, authedUser: %s, user: %s, realUser: %s", principal, authedUser, user, realUser)
	return syscall.EACCES
}

func (k *kerberos) issue(ctx meta.Context, m meta.Meta, volname, user, renewer string) (uint32, *token, syscall.Errno) {
	vol := k.getVol(volname)
	if vol == nil {
		return 0, nil, syscall.EINVAL
	}
	now := time.Now()
	t := &token{
		User:    user,
		Renewer: renewer,
		Issued:  now.Unix(),
		Expire:  now.Unix() + vol.renew,
	}
	passwd := make([]byte, 20)
	_, _ = io.ReadFull(rand.Reader, passwd)
	t.Password = hex.EncodeToString(passwd)
	id, eno := k.storeToken(ctx, m, t)
	if eno != 0 {
		return 0, nil, eno
	}
	return id, t, 0
}

func (k *kerberos) check(ctx meta.Context, m meta.Meta, volname, user string, id uint32, password string) syscall.Errno {
	t, eno := k.loadToken(ctx, m, id)
	if eno != 0 {
		return eno
	}
	now := time.Now().Unix()
	if now > t.Expire {
		logger.Warnf("token %d expired", id)
		return syscall.EINVAL
	}
	if password != t.Password || user != t.User {
		logger.Warnf("token %d invalid user or password", id)
		return syscall.EACCES
	}
	return 0
}

func (k *kerberos) renew(ctx meta.Context, m meta.Meta, volname, renewer string, id uint32, password string) (int64, syscall.Errno) {
	t, eno := k.loadToken(ctx, m, id)
	if eno != 0 {
		return 0, eno
	}
	if password != t.Password || renewer != t.Renewer {
		return 0, syscall.EACCES
	}
	now := time.Now().Unix()
	if now > t.Expire {
		logger.Warnf("token %d expired for renew", id)
		return 0, syscall.EINVAL
	}
	vol := k.getVol(volname)
	t.Expire = min(t.Issued+vol.life, t.Expire+vol.renew)
	eno = k.updateToken(ctx, m, id, t)
	if eno != 0 {
		return 0, eno
	}
	return t.Expire, 0
}

func (k *kerberos) storeToken(ctx meta.Context, m meta.Meta, t *token) (id uint32, st syscall.Errno) {
	marshal, err := json.Marshal(t)
	if err != nil {
		logger.Errorf("marshal token: %s", err)
		return 0, syscall.EINVAL
	}
	return m.StoreToken(ctx, marshal)
}

func (k *kerberos) updateToken(ctx meta.Context, m meta.Meta, id uint32, t *token) syscall.Errno {
	marshal, err := json.Marshal(t)
	if err != nil {
		logger.Errorf("marshal token: %s", err)
		return syscall.EINVAL
	}
	return m.UpdateToken(ctx, id, marshal)
}

func (k *kerberos) loadToken(ctx meta.Context, m meta.Meta, id uint32) (*token, syscall.Errno) {
	tb, errno := m.LoadToken(ctx, id)
	if errno != 0 {
		return nil, errno
	}
	t := &token{}
	err := json.Unmarshal(tb, t)
	if err != nil {
		logger.Errorf("unmarshal token %d: %s", id, err)
		return nil, syscall.EINVAL
	}
	return t, 0
}

func (k *kerberos) cancelToken(ctx meta.Context, m meta.Meta, user string, id uint32, password string) syscall.Errno {
	t, eno := k.loadToken(ctx, m, id)
	if eno != 0 {
		return eno
	}
	if password != t.Password || user != t.Renewer && user != t.User {
		return syscall.EACCES
	}
	return m.DeleteTokens(ctx, []uint32{id})
}

func (k *kerberos) cleanupTokens() {
	var metas []meta.Meta
	k.mu.Lock()
	for _, vol := range k.vols {
		metas = append(metas, vol.m)
	}
	k.mu.Unlock()
	for _, m := range metas {
		ctx := meta.Background()
		tokens, eno := m.ListTokens(ctx)
		if eno != 0 {
			logger.Errorf("list tokens: %s", eno)
			return
		}
		var todelete []uint32
		now := time.Now().Unix()
		for id, data := range tokens {
			t := &token{}
			err := json.Unmarshal(data, t)
			if err != nil {
				logger.Warnf("unmarshal token %d: %s", id, err)
			}
			if t.Expire <= now {
				todelete = append(todelete, id)
			}
		}
		if len(todelete) == 0 {
			return
		}
		logger.Infof("cleaning up %d expired tokens", len(todelete))
		eno = m.DeleteTokens(ctx, todelete)
		if eno != 0 {
			logger.Errorf("delete tokens: %s", eno)
		}
	}
}

func (k *kerberos) loadConf(name, content string, jfs *fs.FileSystem) {
	vol := &volParams{
		m:       jfs.Meta(),
		life:    defaultLife,
		renew:   defaultRenew,
		proxies: make(map[string]*proxyParam),
	}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "#")
		if idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		fields := strings.SplitN(line, "=", 2)
		if len(fields) != 2 {
			logger.Warningf("bad line: %s", line)
			continue
		}
		key := strings.TrimSpace(fields[0])
		value := strings.TrimSpace(fields[1])
		split := strings.Split(key, ".")
		if len(split) < 2 {
			logger.Warningf("bad line: %s", line)
			continue
		}
		keySuffix := split[len(split)-1]
		volName := split[0]
		if volName != name {
			continue
		}
		vol.parse(keySuffix, key, value)
	}
	jfs.Superuser = vol.superuser
	jfs.Supergroup = vol.supergroup
	k.mu.Lock()
	k.vols[name] = vol
	k.mu.Unlock()
}

func (k *kerberos) init() int {
	k.vols = make(map[string]*volParams)
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			k.cleanupTokens()
		}
	}()
	return 0
}

var kerb = kerberos{}
