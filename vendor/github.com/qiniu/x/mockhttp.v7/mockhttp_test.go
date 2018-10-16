package mockhttp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"qiniupkg.com/x/mockhttp.v7"
	"qiniupkg.com/x/rpc.v7"
)

// --------------------------------------------------------------------

func reply(w http.ResponseWriter, code int, data interface{}) {

	msg, _ := json.Marshal(data)
	h := w.Header()
	h.Set("Content-Length", strconv.Itoa(len(msg)))
	h.Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(msg)
}

// --------------------------------------------------------------------

type FooRet struct {
	A int    `json:"a"`
	B string `json:"b"`
	C string `json:"c"`
}

type HandleRet map[string]string

type FooServer struct{}

func (p *FooServer) foo(w http.ResponseWriter, req *http.Request) {
	reply(w, 200, &FooRet{1, req.Host, req.URL.Path})
}

func (p *FooServer) handle(w http.ResponseWriter, req *http.Request) {
	reply(w, 200, HandleRet{"foo": "1", "bar": "2"})
}

func (p *FooServer) postDump(w http.ResponseWriter, req *http.Request) {
	req.Body.Close()
	io.Copy(w, req.Body)
}

func (p *FooServer) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/foo", func(w http.ResponseWriter, req *http.Request) { p.foo(w, req) })
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) { p.handle(w, req) })
	mux.HandleFunc("/dump", func(w http.ResponseWriter, req *http.Request) { p.postDump(w, req) })
}

// --------------------------------------------------------------------

func TestBasic(t *testing.T) {

	server := new(FooServer)
	server.RegisterHandlers(http.DefaultServeMux)

	mockhttp.ListenAndServe("foo.com", nil)

	c := rpc.Client{mockhttp.DefaultClient}
	{
		var foo FooRet
		err := c.Call(nil, &foo, "POST", "http://foo.com/foo")
		if err != nil {
			t.Fatal("call foo failed:", err)
		}
		if foo.A != 1 || foo.B != "foo.com" || foo.C != "/foo" {
			t.Fatal("call foo: invalid ret")
		}
		fmt.Println(foo)
	}
	{
		var ret map[string]string
		err := c.Call(nil, &ret, "POST", "http://foo.com/bar")
		if err != nil {
			t.Fatal("call foo failed:", err)
		}
		if ret["foo"] != "1" || ret["bar"] != "2" {
			t.Fatal("call bar: invalid ret")
		}
		fmt.Println(ret)
	}
	{
		resp, err := c.Post("http://foo.com/dump", "", nil)
		if err != nil {
			t.Fatal("post foo failed:", err)
		}
		resp.Body.Close()
		resp, err = c.Post("http://foo.com/dump", "", strings.NewReader("abc"))
		if err != nil {
			t.Fatal("post foo failed:", err)
		}
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatal("ioutil.ReadAll:", err)
		}
		if len(b) != 0 {
			t.Fatal("body should be empty:", string(b))
		}
	}
}

// --------------------------------------------------------------------

