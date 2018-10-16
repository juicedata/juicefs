package dnscache

import (
  "net"
  "sort"
  "time"
  "testing"
)

func TestFetchReturnsAndErrorOnInvalidLookup(t *testing.T) {
  ips, err := New(0).Lookup("invalid.viki.io")
  if ips != nil {
    t.Errorf("Expecting nil ips, got %v", ips)
  }
  expected := "lookup invalid.viki.io: no such host"
  if err.Error() != expected {
    t.Errorf("Expecting %q error, got %q", expected, err.Error())
  }
}

func TestFetchReturnsAListOfIps(t *testing.T) {
  ips, _ := New(0).Lookup("dnscache.go.test.viki.io")
  assertIps(t, ips, []string{"1.123.58.13", "31.85.32.110"})
}

func TestCallingLookupAddsTheItemToTheCache(t *testing.T) {
  r := New(0)
  r.Lookup("dnscache.go.test.viki.io")
  assertIps(t, r.cache["dnscache.go.test.viki.io"], []string{"1.123.58.13", "31.85.32.110"})
}

func TestFetchLoadsValueFromTheCache(t *testing.T) {
  r := New(0)
  r.cache["invalid.viki.io"] = []net.IP{net.ParseIP("1.1.2.3")}
  ips, _ := r.Fetch("invalid.viki.io")
  assertIps(t, ips, []string{"1.1.2.3"})
}

func TestFetchOneLoadsTheFirstValue(t *testing.T) {
  r := New(0)
  r.cache["something.viki.io"] = []net.IP{net.ParseIP("1.1.2.3"), net.ParseIP("100.100.102.103")}
  ip, _ := r.FetchOne("something.viki.io")
  assertIps(t, []net.IP{ip}, []string{"1.1.2.3"})
}

func TestFetchOneStringLoadsTheFirstValue(t *testing.T) {
  r := New(0)
  r.cache["something.viki.io"] = []net.IP{net.ParseIP("100.100.102.103"), net.ParseIP("100.100.102.104")}
  ip, _ := r.FetchOneString("something.viki.io")
  if ip != "100.100.102.103" {
    t.Errorf("expected 100.100.102.103 but got %v", ip)
  }
}

func TestFetchLoadsTheIpAndCachesIt(t *testing.T) {
  r := New(0)
  ips, _ := r.Fetch("dnscache.go.test.viki.io")
  assertIps(t, ips, []string{"1.123.58.13", "31.85.32.110"})
  assertIps(t, r.cache["dnscache.go.test.viki.io"], []string{"1.123.58.13", "31.85.32.110"})
}

func TestItReloadsTheIpsAtAGivenInterval(t *testing.T) {
  r := New(1)
  r.cache["dnscache.go.test.viki.io"] = nil
  time.Sleep(time.Second * 2)
  assertIps(t, r.cache["dnscache.go.test.viki.io"], []string{"1.123.58.13", "31.85.32.110"})
}

func assertIps(t *testing.T, actuals []net.IP, expected []string) {
  if len(actuals) != len(expected) {
    t.Errorf("Expecting %d ips, got %d", len(expected), len(actuals))
  }
  sort.Strings(expected)
  for _, ip := range actuals {
    if sort.SearchStrings(expected, ip.String()) == -1 {
      t.Errorf("Got an unexpected ip: %v:", actuals[0])
    }
  }
}
