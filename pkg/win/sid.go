//go:build windows
// +build windows

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package win

import (
	"fmt"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/juicedata/juicefs/pkg/utils"

	"golang.org/x/sys/windows"
)

var logger = utils.GetLogger("juicefs")

var (
	modadvapi32                   = windows.NewLazySystemDLL("advapi32.dll")
	procLsaOpenPolicy             = modadvapi32.NewProc("LsaOpenPolicy")
	procLsaQueryInformationPolicy = modadvapi32.NewProc("LsaQueryInformationPolicy")
	procLsaFreeMemory             = modadvapi32.NewProc("LsaFreeMemory")
	procLsaClose                  = modadvapi32.NewProc("LsaClose")

	netapi32 = windows.NewLazySystemDLL("netapi32.dll")

	//https://learn.microsoft.com/en-us/windows/win32/api/dsgetdc/nf-dsgetdc-dsenumeratedomaintrustsw
	procDsEnumerateDomainTrustsW = netapi32.NewProc("DsEnumerateDomainTrustsW")
	procNetApiBufferFree         = netapi32.NewProc("NetApiBufferFree")
)

var trustedDomains []trustedDomain

type LSA_OBJECT_ATTRIBUTES struct {
	Length                   uint32
	RootDirectory            windows.Handle
	ObjectName               uintptr
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

var primaryDomainSid *windows.SID = nil
var accountDomainSid *windows.SID = nil

const (
	PolicyAccountDomainInformation = 5
	PolicyDnsDomainInformation     = 12
)

const (
	POLICY_VIEW_LOCAL_INFORMATION   = 0x00000001
	POLICY_VIEW_AUDIT_INFORMATION   = 0x00000002
	POLICY_GET_PRIVATE_INFORMATION  = 0x00000004
	POLICY_TRUST_ADMIN              = 0x00000008
	POLICY_CREATE_ACCOUNT           = 0x00000010
	POLICY_CREATE_SECRET            = 0x00000020
	POLICY_CREATE_PRIVILEGE         = 0x00000040
	POLICY_SET_DEFAULT_QUOTA_LIMITS = 0x00000080
	POLICY_SET_AUDIT_REQUIREMENTS   = 0x00000100
	POLICY_AUDIT_LOG_ADMIN          = 0x00000200
	POLICY_SERVER_ADMIN             = 0x00000400
	POLICY_LOOKUP_NAMES             = 0x00000800
	POLICY_NOTIFICATION             = 0x00001000
)

const (
	AdministratorUIDFromFUSE = 197108 // This is calcuated from the SID of Administrator user on Windows. //0x30000 + 500
	AdminstratorsGIDFromFUSE = 544    //  S-1-5-32-544
	SystemUIDFromFUSE        = 18     //  S-1-5-32-18
)

type UNICODE_STRING struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

// https://learn.microsoft.com/en-us/windows/win32/api/lsalookup/ns-lsalookup-policy_account_domain_info
type POLICY_ACCOUNT_DOMAIN_INFO struct {
	DomainName UNICODE_STRING
	DomainSid  *windows.SID
}

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// https://learn.microsoft.com/en-us/windows/win32/api/lsalookup/ns-lsalookup-policy_dns_domain_info
type POLICY_DNS_DOMAIN_INFO struct {
	Name          UNICODE_STRING
	DnsDomainName UNICODE_STRING
	DnsForestName UNICODE_STRING
	DomainGuid    GUID
	Sid           *windows.SID
}

const (
	DS_DOMAIN_DIRECT_INBOUND  = 0x0001
	DS_DOMAIN_DIRECT_OUTBOUND = 0x0002
	DS_DOMAIN_IN_FOREST       = 0x0008
)

type DS_DOMAIN_TRUSTSW struct {
	NetbiosDomainName *uint16      // LPWSTR
	DnsDomainName     *uint16      // LPWSTR
	Flags             uint32       // ULONG
	ParentIndex       uint32       // ULONG
	TrustType         uint32       // ULONG
	TrustAttributes   uint32       // ULONG
	DomainSid         *windows.SID // PSID
	DomainGuid        windows.GUID // GUID
}

type trustedDomain struct {
	DomainSid         *windows.SID
	NetbiosDomainName *uint16
	DnsDomainName     *uint16
	TrustPosixOffset  uint32
}

func IsRelativeSid(sid1 *windows.SID, sid2 *windows.SID) bool {
	if sid1 == nil || sid2 == nil {
		return sid1 == sid2
	}

	// Check if the SIDs have the same revision, we have to do it by ourself
	// since windows.SID does not expose the revision field directly.
	rev1 := *(*uint8)(unsafe.Pointer(sid1))
	rev2 := *(*uint8)(unsafe.Pointer(sid2))
	if rev1 != rev2 {
		return false
	}

	auth1 := sid1.IdentifierAuthority()
	auth2 := sid2.IdentifierAuthority()
	for i := 0; i < len(auth1.Value); i++ {
		if auth1.Value[i] != auth2.Value[i] {
			return false
		}
	}

	cnt1 := sid1.SubAuthorityCount()
	cnt2 := sid2.SubAuthorityCount()
	if cnt1+1 != cnt2 {
		return false
	}

	for i := uint8(0); i < cnt1; i++ {
		if sid1.SubAuthority(uint32(i)) != sid2.SubAuthority(uint32(i)) {
			return false
		}
	}

	return true
}

// initializeTrustPosixOffsets queries LDAP and sets TrustPosixOffset for each trusted domain.
func initializeTrustPosixOffsets() error {
	handle, err := LdapConnect("") // empty string means default server
	if err != nil {
		return fmt.Errorf("LdapConnect failed: %w", err)
	}
	defer LdapClose(handle)

	defaultNC, err := LdapGetDefaultNamingContext(handle)
	if err != nil {
		return fmt.Errorf("LdapGetDefaultNamingContext failed: %w", err)
	}

	// For each trusted domain, get trustPosixOffset
	for i := range trustedDomains {
		domain := windows.UTF16PtrToString(trustedDomains[i].DnsDomainName)
		offsetStr, err := LdapGetTrustPosixOffset(handle, defaultNC, domain)
		if err == nil {
			if val, err := strconv.ParseUint(offsetStr, 10, 32); err == nil {
				trustedDomains[i].TrustPosixOffset = uint32(val)
			}
		}
	}

	// If trustPosixOffset looks wrong, fix it up using Cygwin magic value 0xfe500000
	for i := range trustedDomains {
		if trustedDomains[i].TrustPosixOffset < 0x100000 {
			trustedDomains[i].TrustPosixOffset = 0xfe500000
		}
	}

	return nil
}

func init() {
	if runtime.GOOS != "windows" {
		return
	}

	var objAttr LSA_OBJECT_ATTRIBUTES
	objAttr.Length = uint32(unsafe.Sizeof(objAttr))

	var policyHandle windows.Handle
	r1, _, _ := procLsaOpenPolicy.Call(
		0,
		uintptr(unsafe.Pointer(&objAttr)),
		uintptr(POLICY_VIEW_LOCAL_INFORMATION),
		uintptr(unsafe.Pointer(&policyHandle)),
	)
	if windows.NTStatus(r1) != windows.STATUS_SUCCESS {
		return
	}
	defer procLsaClose.Call(uintptr(policyHandle))

	// Get the account domain SID
	var acctInfoPtr uintptr
	r1, _, _ = procLsaQueryInformationPolicy.Call(
		uintptr(policyHandle),
		uintptr(PolicyAccountDomainInformation),
		uintptr(unsafe.Pointer(&acctInfoPtr)),
	)
	if windows.NTStatus(r1) == windows.STATUS_SUCCESS && acctInfoPtr != 0 {
		defer procLsaFreeMemory.Call(acctInfoPtr)
		info := (*POLICY_ACCOUNT_DOMAIN_INFO)(unsafe.Pointer(acctInfoPtr))
		if info.DomainSid != nil {
			if sidCopy, err := info.DomainSid.Copy(); err == nil {
				accountDomainSid = sidCopy
			}
		}
	}

	// Get the primary domain SID
	var primInfoPtr uintptr
	r1, _, _ = procLsaQueryInformationPolicy.Call(
		uintptr(policyHandle),
		uintptr(PolicyDnsDomainInformation),
		uintptr(unsafe.Pointer(&primInfoPtr)),
	)
	if windows.NTStatus(r1) == windows.STATUS_SUCCESS && primInfoPtr != 0 {
		defer procLsaFreeMemory.Call(primInfoPtr)
		info2 := (*POLICY_DNS_DOMAIN_INFO)(unsafe.Pointer(primInfoPtr))
		if info2.Sid != nil {
			if sidCopy, err := info2.Sid.Copy(); err == nil {
				primaryDomainSid = sidCopy
			}
		}
	}

	// QUERY trusted domains
	var domainsPtr uintptr
	var domainCount uint32
	r1, _, _ = procDsEnumerateDomainTrustsW.Call(
		0,
		uintptr(DS_DOMAIN_DIRECT_INBOUND|DS_DOMAIN_DIRECT_OUTBOUND|DS_DOMAIN_IN_FOREST),
		uintptr(unsafe.Pointer(&domainsPtr)),
		uintptr(unsafe.Pointer(&domainCount)),
	)
	if r1 != 0 || domainsPtr == 0 {
		return
	}
	defer procNetApiBufferFree.Call(domainsPtr)

	entrySize := unsafe.Sizeof(DS_DOMAIN_TRUSTSW{})
	base := domainsPtr
	realCount := 0
	for i := 0; i < int(domainCount); i++ {
		dom := (*DS_DOMAIN_TRUSTSW)(unsafe.Pointer(base + uintptr(i)*entrySize))
		if dom.DomainSid == nil ||
			(dom.NetbiosDomainName == nil && dom.DnsDomainName == nil) ||
			windows.EqualSid(dom.DomainSid, primaryDomainSid) {
			continue
		}
		realCount++
	}

	trustedDomains = make([]trustedDomain, 0, realCount)
	for i := 0; i < int(domainCount); i++ {
		dom := (*DS_DOMAIN_TRUSTSW)(unsafe.Pointer(base + uintptr(i)*entrySize))
		if dom.DomainSid == nil ||
			(dom.NetbiosDomainName == nil && dom.DnsDomainName == nil) ||
			windows.EqualSid(dom.DomainSid, primaryDomainSid) {
			continue
		}

		sidCopy, err := dom.DomainSid.Copy()
		if err != nil {
			continue
		}

		trustedDomains = append(trustedDomains, trustedDomain{
			DomainSid:         sidCopy,
			NetbiosDomainName: dom.NetbiosDomainName,
			DnsDomainName:     dom.DnsDomainName,
			TrustPosixOffset:  0,
		})
	}

	if len(trustedDomains) != 0 {
		initializeTrustPosixOffsets()
	}
}

func ConvertSidStrToUid(sidStr string) (int, error) {
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return -1, err
	}
	ret := convertSidToUid(sid)
	if ret < 0 {
		return -1, fmt.Errorf("invalid uid %d for sid %s", ret, sidStr)
	}
	return ret, nil
}

func convertSidToUid(sid *windows.SID) int {
	if sid == nil || !sid.IsValid() {
		logger.Trace("GetCurrentUID sid is invalid or nil")
		return -1
	}

	subAuthCount := sid.SubAuthorityCount()
	if subAuthCount == 0 {
		logger.Trace("subAuthCount is 0")
		return -1
	}

	// SID FORMAT: https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-security-identifiers
	// S-VERSION-IDENTIFIER_AUTHORITY-SUBAUTHORITY1-SUBAUTHORITY2-...-SUBAUTHORITYn(RID)
	// SUBAUTHORITY1-SUBAUTHORITY2 also known as Domain Identifier

	rid := sid.SubAuthority(uint32(subAuthCount - 1))
	subAuth0 := sid.SubAuthority(0)
	auth := sid.IdentifierAuthority()

	logger.Tracef("GetCurrentUID: subAuthCount=%d, rid=%d, subAuth0=%d, auth=%v", subAuthCount, rid, subAuth0, auth)

	ret := -1

	if auth == windows.SECURITY_NT_AUTHORITY {
		// windows.SECURITY_NT_AUTHORITY: 5
		if subAuthCount == 1 {
			// well-known SIDs
			ret = int(rid)
		} else if subAuthCount == 2 && subAuth0 == 32 {
			// well-known SIDs
			ret = int(rid) // BUILTIN domain
		} else if subAuthCount >= 2 && subAuth0 == 5 {
			// ignore
		} else if subAuthCount >= 5 && subAuth0 == 21 {
			if primaryDomainSid != nil && IsRelativeSid(primaryDomainSid, sid) {
				// Accounts from the machine's primary domain:
				ret = 0x100000 + int(rid)
			} else if accountDomainSid != nil && IsRelativeSid(accountDomainSid, sid) {
				// Accounts from the local machine's user DB (SAM):
				ret = 0x30000 + int(rid)
			} else {
				// Accounts from a trusted domain of the machine's primary domain:
				for _, dom := range trustedDomains {
					if IsRelativeSid(dom.DomainSid, sid) {
						ret = int(dom.TrustPosixOffset) + int(rid)
						break
					}
				}
			}
		} else if subAuthCount == 2 {
			// Other well-known SIDs in the NT_AUTHORITY domain (S-1-5-X-RID):
			ret = 0x1000 + int(subAuth0) + int(rid)
		}
	} else if auth == windows.SECURITY_MANDATORY_LABEL_AUTHORITY {
		// windows.SECURITY_MANDATORY_LABEL_AUTHORITY: 16
		ret = 0x60000 + int(rid)
	} else if auth.Value[5] != 0 || rid != 65534 {
		// Other well-known SIDs:
		ret = 0x10000 + 0x100*int(auth.Value[5]) + int(rid)
	}

	if ret == -1 {
		ret = 65534 // fallback to unmapped SID
	}

	logger.Tracef("GetCurrentUID: returning %d for sid %s", ret, sid.String())

	return ret
}

func GetCurrentUserSID() (*windows.SID, error) {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return nil, err
	}
	defer token.Close()

	var requiredLen uint32
	err = windows.GetTokenInformation(token, windows.TokenUser, nil, 0, &requiredLen)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, err
	}

	buf := make([]byte, requiredLen)
	err = windows.GetTokenInformation(token, windows.TokenUser, &buf[0], requiredLen, &requiredLen)
	if err != nil {
		return nil, err
	}
	userInfo := (*windows.Tokenuser)(unsafe.Pointer(&buf[0]))
	return userInfo.User.Sid, nil
}

func GetCurrentUserPrimaryGroupSID() (*windows.SID, error) {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return nil, err
	}
	defer token.Close()

	var requiredLen uint32
	err = windows.GetTokenInformation(token, windows.TokenPrimaryGroup, nil, 0, &requiredLen)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, err
	}

	buf := make([]byte, requiredLen)
	err = windows.GetTokenInformation(token, windows.TokenPrimaryGroup, &buf[0], requiredLen, &requiredLen)
	if err != nil {
		return nil, err
	}
	groupInfo := (*windows.Tokenprimarygroup)(unsafe.Pointer(&buf[0]))
	return groupInfo.PrimaryGroup, nil
}

func GetCurrentUID() int {
	// convert sid to uid, this function have the same procedure with FspPosixMapSidToUid to keep consistencywin
	// https://cygwin.com/cygwin-ug-net/ntsec.html

	sid, err := GetCurrentUserSID()
	if err != nil {
		logger.Warnf("failed to get sid for current user, %s", err)
		return -1
	}

	return convertSidToUid(sid)
}

func GetCurrentGID() int {
	sid, err := GetCurrentUserPrimaryGroupSID()
	if err != nil {
		logger.Warnf("failed to get primary group sid for current user, %s", err)
		return -1
	}

	return convertSidToUid(sid)
}

func GetCurrentGroupName() string {
	sid, err := GetCurrentUserPrimaryGroupSID()
	if err != nil {
		logger.Warnf("failed to get sid for current user, %s", err)
		return ""
	}
	return GetSidName(sid, false)
}

func GetSidName(sid *windows.SID, withDomain bool) string {
	var nameLen, domLen, sidType uint32

	err := windows.LookupAccountSid(
		nil, sid,
		nil, &nameLen,
		nil, &domLen,
		&sidType,
	)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return sid.String()
	}

	name := make([]uint16, nameLen)
	dom := make([]uint16, domLen)

	err = windows.LookupAccountSid(
		nil, sid,
		&name[0], &nameLen,
		&dom[0], &domLen,
		&sidType,
	)
	if err != nil {
		logger.Warnf("LookupAccountSid failed: %s", err)
		return sid.String()
	}

	account := syscall.UTF16ToString(name)
	if withDomain {
		domain := syscall.UTF16ToString(dom)
		return domain + `\` + account
	}

	return account
}

func IsProcessElevated() (bool, error) {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false, err
	}
	defer token.Close()

	// https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-token_elevation
	type tokenElevation struct {
		TokenIsElevated uint32
	}

	var elevation tokenElevation
	var outLen uint32
	err = windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevation)), uint32(unsafe.Sizeof(elevation)), &outLen)
	if err != nil {
		return false, err
	}

	return elevation.TokenIsElevated != 0, nil
}
