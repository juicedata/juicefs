//go:build windows
// +build windows

package win

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modWldap32           = windows.NewLazySystemDLL("wldap32.dll")
	procLdapInitW        = modWldap32.NewProc("ldap_initW")
	procLdapSetOptionW   = modWldap32.NewProc("ldap_set_optionW")
	procLdapBindSW       = modWldap32.NewProc("ldap_bind_sW")
	procLdapUnbind       = modWldap32.NewProc("ldap_unbind")
	procLdapSearchSW     = modWldap32.NewProc("ldap_search_sW")
	procLdapFirstEntryW  = modWldap32.NewProc("ldap_first_entryW")
	procLdapGetValuesW   = modWldap32.NewProc("ldap_get_valuesW")
	procLdapCountValuesW = modWldap32.NewProc("ldap_count_valuesW")
	procLdapValueFreeW   = modWldap32.NewProc("ldap_value_freeW")
	procLdapMsgFreeW     = modWldap32.NewProc("ldap_msgfreeW")
)

// from winldap.h
const (
	LDAP_PORT           = 389
	LDAP_SUCCESS        = 0
	LDAP_OPT_SIGN       = 0x95
	LDAP_OPT_ENCRYPT    = 0x96
	LDAP_OPT_ON         = 1
	LDAP_SCOPE_BASE     = 0x00
	LDAP_SCOPE_ONELEVEL = 0x01
	LDAP_AUTH_NEGOTIATE = 0x0486 // LDAP_AUTH_OTHERKIND (0x86) | 0x0400
)

func LdapConnect(host string) (uintptr, error) {
	hostPtr, err := windows.UTF16PtrFromString(host)
	if err != nil {
		return 0, err
	}
	handle, _, _ := procLdapInitW.Call(
		uintptr(unsafe.Pointer(hostPtr)),
		uintptr(LDAP_PORT),
	)
	if handle == 0 {
		return 0, fmt.Errorf("ldap_initW failed")
	}
	procLdapSetOptionW.Call(handle, uintptr(LDAP_OPT_SIGN), uintptr(LDAP_OPT_ON))
	procLdapSetOptionW.Call(handle, uintptr(LDAP_OPT_ENCRYPT), uintptr(LDAP_OPT_ON))

	r1, _, _ := procLdapBindSW.Call(handle, 0, 0, uintptr(LDAP_AUTH_NEGOTIATE))
	if int32(r1) != LDAP_SUCCESS {
		procLdapUnbind.Call(handle)
		return 0, fmt.Errorf("ldap_bind_sW failed: %d", r1)
	}
	return handle, nil
}

func LdapClose(handle uintptr) {
	procLdapUnbind.Call(handle)
}

func LdapGetValue(
	handle uintptr,
	base string,
	scope uint32,
	filter string,
	attribute string,
) (string, error) {
	var basePtr *uint16
	if base != "" {
		p, err := windows.UTF16PtrFromString(base)
		if err != nil {
			return "", err
		}
		basePtr = p
	}
	filterPtr, err := windows.UTF16PtrFromString(filter)
	if err != nil {
		return "", err
	}
	attrPtr, err := windows.UTF16PtrFromString(attribute)
	if err != nil {
		return "", err
	}
	attrs := []uintptr{uintptr(unsafe.Pointer(attrPtr)), 0}

	var msg uintptr
	r1, _, _ := procLdapSearchSW.Call(
		handle,
		uintptr(unsafe.Pointer(basePtr)),
		uintptr(scope),
		uintptr(unsafe.Pointer(filterPtr)),
		uintptr(unsafe.Pointer(&attrs[0])),
		0,
		uintptr(unsafe.Pointer(&msg)),
	)
	if int32(r1) != LDAP_SUCCESS {
		return "", fmt.Errorf("ldap_search_sW failed: %d", r1)
	}
	defer procLdapMsgFreeW.Call(msg)

	entry, _, _ := procLdapFirstEntryW.Call(handle, msg)
	if entry == 0 {
		return "", fmt.Errorf("no entries found")
	}
	vals, _, _ := procLdapGetValuesW.Call(handle, entry, uintptr(unsafe.Pointer(attrPtr)))
	if vals == 0 {
		return "", fmt.Errorf("no attribute values")
	}
	defer procLdapValueFreeW.Call(vals)
	cnt, _, _ := procLdapCountValuesW.Call(vals)
	if cnt == 0 {
		return "", fmt.Errorf("no attribute values")
	}
	firstPtr := *(*uintptr)(unsafe.Pointer(vals))
	value := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(firstPtr)))
	return value, nil
}

func LdapGetDefaultNamingContext(handle uintptr) (string, error) {
	return LdapGetValue(handle, "", LDAP_SCOPE_BASE, "(objectClass=*)", "defaultNamingContext")
}

func LdapGetTrustPosixOffset(
	handle uintptr,
	context string,
	domain string,
) (string, error) {
	isFlat := !strings.Contains(domain, ".")
	base := fmt.Sprintf("CN=System,%s", context)
	var filter string
	if isFlat {
		filter = fmt.Sprintf("(&(objectClass=trustedDomain)(flatName=%s))", domain)
	} else {
		filter = fmt.Sprintf("(&(objectClass=trustedDomain)(name=%s))", domain)
	}
	return LdapGetValue(handle, base, LDAP_SCOPE_ONELEVEL, filter, "trustPosixOffset")
}
