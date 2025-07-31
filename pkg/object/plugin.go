package object

import (
	"bytes"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/jiefenghuang/jfs-plugin/pkg/msg"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type PluginOptions struct {
	Version  string
	URL      string
	proto    string
	addr     string
	MaxConn  uint
	BuffList []int
	Logger   logrus.FieldLogger
}

func (opt *PluginOptions) Check() error {
	if opt.Logger == nil {
		opt.Logger = utils.GetLogger("plugin-client")
	}
	proto, addr := msg.SplitAddr(opt.URL)
	if proto == "" || addr == "" {
		return errors.Errorf("invalid address format %s, expected 'tcp://<addr>' or 'unix://<path>'", opt.URL)
	}
	opt.proto, opt.addr = proto, addr
	if err := msg.CheckProto(opt.proto); err != nil {
		return err
	}
	if opt.URL == "" {
		return errors.New("address must not be empty")
	}
	if opt.MaxConn == 0 {
		opt.MaxConn = 100
		logger.Warnf("max connection is set to 100")
	}
	if opt.BuffList == nil {
		opt.BuffList = msg.DefaultCliCapList
	}
	if version.Parse(opt.Version) == nil {
		return errors.Errorf("invalid version: %s, format should be like '1.3.0'", opt.Version)
	}
	return nil
}

type PluginClient struct {
	sync.Mutex
	*PluginOptions
	closed  bool
	connCh  chan net.Conn
	wg      sync.WaitGroup
	pool    msg.BytesPool
	authErr error
}

func NewPluginClient(opt *PluginOptions) (*PluginClient, error) {
	if err := opt.Check(); err != nil {
		return nil, err
	}
	return &PluginClient{
		PluginOptions: opt,
		connCh:        make(chan net.Conn, opt.MaxConn),
		pool:          msg.NewBytesPool(opt.BuffList),
	}, nil
}

func (c *PluginClient) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	c.wg.Wait()
	close(c.connCh)
	for conn := range c.connCh {
		_ = conn.Close()
	}
	return nil
}

func (c *PluginClient) getConn() (net.Conn, error) {
	if c.closed {
		return nil, errors.New("client is closed")
	}
	var conn net.Conn
	select {
	case conn = <-c.connCh:
	default:
		c.Lock()
		defer c.Unlock()
		dialer := &net.Dialer{Timeout: time.Second, KeepAlive: time.Minute}
		nConn, err := dialer.Dial(c.proto, c.addr)
		if err != nil {
			return nil, err
		}
		conn = nConn
		if err = c.auth(conn); err != nil {
			c.authErr = err
			_ = conn.Close()
			return nil, errors.New("plugin authentication failed")
		}
	}
	return conn, nil
}

func (c *PluginClient) auth(conn net.Conn) (err error) {
	bodyLen := 2 + len(c.Version)
	buff := c.pool.Get(msg.HeaderLen + bodyLen)
	defer c.pool.Put(buff)
	m := msg.NewEncMsg(buff, bodyLen, msg.CmdAuth)
	m.PutString(c.Version)
	if _, err = conn.Write(m.Bytes()); err != nil {
		return errors.Wrap(err, "failed to write verify request")
	}
	_, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdAuth)
	return err
}

var ne = new(net.OpError)

func (c *PluginClient) call(f func(conn net.Conn) error) error {
	if c.authErr != nil {
		return c.authErr
	}

	c.wg.Add(1)
	defer c.wg.Done()
	conn, err := c.getConn()
	if err != nil {
		return err
	}
	err = f(conn)
	if c.closed || (errors.As(err, &ne) && !ne.Timeout()) {
		_ = conn.Close()
	} else {
		select {
		case c.connCh <- conn:
		default:
			_ = conn.Close()
		}
	}
	return err
}

var _ ObjectStorage = (*PluginClient)(nil)
var _ SupportStorageClass = (*PluginClient)(nil)

func (c *PluginClient) SetStorageClass(sc string) error {
	return c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen + 2 + len(sc))
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, 2+len(sc), msg.CmdSetSC)
		m.PutString(sc)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write SetStorageClass request")
		}
		_, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdSetSC)
		return err
	})
}

func (c *PluginClient) AbortUpload(key string, uploadID string) {
	if err := c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen + 2 + len(key) + 2 + len(uploadID))
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, 2+len(key)+2+len(uploadID), msg.CmdAbortMPU)
		m.PutString(key)
		m.PutString(uploadID)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write AbortUpload request")
		}
		_, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdAbortMPU)
		return err
	}); err != nil {
		c.Logger.Error("failed to call AbortUpload: %v", err)
	}
}

func (c *PluginClient) CompleteUpload(key string, uploadID string, parts []*Part) error {
	return c.call(func(conn net.Conn) error {
		bLen := 2 + len(key) + 2 + len(uploadID) + 4 // last 4 bytes is a placeholder for next batch length
		buff := c.pool.Get(4 + 1<<20)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bLen, msg.CmdCompleteMPU)
		m.PutString(key)
		m.PutString(uploadID)

		off := m.Offset()
		batchLen := 0
		m.Put32(0)

		var err error
		if len(parts) == 0 {
			if _, err = conn.Write(m.Bytes()[:m.Offset()]); err != nil {
				return errors.Wrap(err, "failed to write CompleteUpload request with no parts")
			}
		} else {
			var partLen int
			for _, part := range parts {
				partLen = 2 + len(part.ETag) + 4 + 4
				if m.Left() < partLen {
					c.Logger.Debugf("batch length %d exceeds buffer size, flushing", m.Offset())
					data := m.Bytes()[:m.Offset()]
					m.Seek(off)
					m.Put32(uint32(batchLen))
					if _, err := conn.Write(data); err != nil {
						return errors.Wrap(err, "failed to write CompleteUpload batch")
					}
					m.SetBytes(buff)
					off = m.Offset()
					batchLen = 0
					m.Put32(0)
				}
				m.Put32(uint32(part.Num))
				m.Put32(uint32(part.Size))
				m.PutString(part.ETag)
				batchLen += partLen
			}

			if batchLen > 0 {
				m.Put32(0)
				data := m.Bytes()[:m.Offset()]
				m.Seek(off)
				m.Put32(uint32(batchLen))
				if _, err = conn.Write(data); err != nil {
					return errors.Wrap(err, "failed to write CompleteUpload final batch")
				}
			}
		}

		_, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdCompleteMPU)
		return err
	})
}

func (c *PluginClient) Copy(dst string, src string) error {
	return c.call(func(conn net.Conn) (err error) {
		bLen := 4 + len(dst) + len(src)
		buff := c.pool.Get(msg.HeaderLen + bLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bLen, msg.CmdCopy)
		m.PutString(dst)
		m.PutString(src)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Copy request")
		}

		_, err = c.readResp(conn, buff, msg.CmdCopy)
		return err
	})
}

// readResp need put buff back to pool if msg != nil
func (c *PluginClient) readResp(conn net.Conn, hBuff []byte, cmd byte) (*msg.Msg, error) {
	var err error
	if hBuff == nil || len(hBuff) != msg.HeaderLen {
		hBuff = c.pool.Get(msg.HeaderLen)
		defer c.pool.Put(hBuff)
	}
	if _, err = io.ReadFull(conn, hBuff); err != nil {
		return nil, errors.Wrapf(err, "cmd %s failed to read response header", msg.Cmd2Name[cmd])
	}
	m := msg.NewMsg(hBuff)
	bodyLen, rCmd := m.GetHeader()
	if rCmd != cmd {
		return nil, errors.Errorf("cmd %s got unexpected command in response: %s", msg.Cmd2Name[cmd], msg.Cmd2Name[rCmd])
	}
	if bodyLen == 0 {
		return nil, nil
	}
	buff := c.pool.Get(int(bodyLen))
	if _, err = io.ReadFull(conn, buff); err != nil {
		c.pool.Put(buff)
		return nil, errors.Wrapf(err, "cmd %s failed to read response body", msg.Cmd2Name[cmd])
	}
	m.SetBytes(buff)
	errMsg := m.GetString()
	if errMsg != "" {
		err = errors.Errorf("cmd %s failed: %s", msg.Cmd2Name[cmd], errMsg)
	}
	if !m.HasMore() {
		c.pool.Put(buff)
		return nil, err
	}
	return m, err
}

func (c *PluginClient) Create() error {
	return c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen)
		m := msg.NewEncMsg(buff, 0, msg.CmdCreate)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Create request")
		}
		_, err = c.readResp(conn, buff, msg.CmdCreate)
		return err
	})
}

func (c *PluginClient) CreateMultipartUpload(key string) (*MultipartUpload, error) {
	var mpu *MultipartUpload
	err := c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen + 2 + len(key))
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, 2+len(key), msg.CmdCreateMPU)
		m.PutString(key)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write CreateMultipartUpload request")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdCreateMPU)
		if err != nil {
			return err
		}
		defer c.pool.Put(m.Bytes())
		mpu = &MultipartUpload{
			MinPartSize: int(m.Get32()),
			MaxCount:    int(m.Get32()),
			UploadID:    m.GetString(),
		}
		return nil
	})
	return mpu, err
}

func (c *PluginClient) Delete(key string, getters ...AttrGetter) error {
	return c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen + 2 + len(key))
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, 2+len(key), msg.CmdDel)
		m.PutString(key)
		if _, err := conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Delete request")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdDel)
		if m != nil {
			attrs := ApplyGetters(getters...)
			attrs.SetStorageClass(m.GetString())
			c.pool.Put(m.Bytes())
		}
		return err
	})
}

type buffReader struct {
	*bytes.Buffer
	pool     msg.BytesPool
	fullData []byte
}

func newBuffReader(data []byte, fullData []byte, pool msg.BytesPool) *buffReader {
	return &buffReader{
		Buffer:   bytes.NewBuffer(data),
		fullData: fullData,
		pool:     pool,
	}
}

func (b *buffReader) Close() error {
	b.pool.Put(b.fullData)
	return nil
}

var _ io.ReadCloser = (*buffReader)(nil)

func (c *PluginClient) Get(key string, off int64, limit int64, getters ...AttrGetter) (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := c.call(func(conn net.Conn) (err error) {
		bLen := 2 + len(key) + 16
		buff := c.pool.Get(msg.HeaderLen + bLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bLen, msg.CmdGet)
		m.PutString(key)
		m.Put64(uint64(off))
		attrs := ApplyGetters(getters...)
		if limit <= 0 && attrs.GetRequestSize() > 0 {
			m.Put64(uint64(attrs.GetRequestSize()))
		} else {
			m.Put64(uint64(limit))
		}

		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Get header")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdGet)
		if m != nil {
			rid, sc := m.GetString(), m.GetString()
			attrs.SetRequestID(rid).SetStorageClass(sc)
		}
		if err != nil {
			c.pool.Put(m.Bytes())
			return err
		}
		rc = newBuffReader(m.Buffer.Buffer(), m.Bytes(), c.pool)
		return nil
	})
	return rc, err
}

func (c *PluginClient) Head(key string) (Object, error) {
	var o Object
	err := c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen + 2 + len(key))
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, 2+len(key), msg.CmdHead)
		m.PutString(key)

		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Head request")
		}

		m, err = c.readResp(conn, buff, msg.CmdHead)
		if err != nil {
			return err
		}
		o = &obj{
			key:   key,
			sc:    m.GetString(),
			size:  int64(m.Get64()),
			mtime: time.Unix(0, int64(m.Get64())),
			isDir: m.GetBool(),
		}
		c.pool.Put(m.Bytes())
		return nil
	})
	return o, err
}

func (c *PluginClient) Limits() Limits {
	var limit Limits
	if err := c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, 0, msg.CmdLimits)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Limits request")
		}

		m, err = c.readResp(conn, buff, msg.CmdLimits)
		limit.IsSupportMultipartUpload = m.GetBool()
		limit.IsSupportUploadPartCopy = m.GetBool()
		limit.MinPartSize = int(m.Get64())
		limit.MaxPartSize = int64(m.Get64())
		limit.MaxPartCount = int(m.Get64())
		c.pool.Put(m.Bytes())
		return err
	}); err != nil {
		c.Logger.Errorf("failed to call Limits: %v", err)
	}
	return limit
}

func (c *PluginClient) List(prefix string, startAfter string, token string, delimiter string, limit int64, followLink bool) ([]Object, bool, string, error) {
	var objs []Object
	isTruncated := false
	nextMarker := ""
	err := c.call(func(conn net.Conn) (err error) {
		bodyLen := 8 + len(prefix) + len(startAfter) + len(token) + len(delimiter) + 9
		buff := c.pool.Get(msg.HeaderLen + bodyLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bodyLen, msg.CmdList)
		m.PutString(prefix)
		m.PutString(startAfter)
		m.PutString(token)
		m.PutString(delimiter)
		m.Put64(uint64(limit))
		m.PutBool(followLink)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write List request")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdList)
		if err != nil {
			return err
		}
		defer c.pool.Put(m.Bytes())
		isTruncated = m.GetBool()
		nextMarker = m.GetString()
		batchLen := m.Get32()

		batchBuff := c.pool.Get(1<<20 + 4)
		defer c.pool.Put(batchBuff)
		for {
			if batchLen == 0 {
				break
			}
			if _, err = io.ReadFull(conn, batchBuff[:batchLen+4]); err != nil {
				return errors.Wrapf(err, "cmd %s failed to read response body", msg.Cmd2Name[msg.CmdList])
			}
			m.SetBytes(batchBuff[:batchLen])
			for m.HasMore() {
				objs = append(objs, &obj{
					key:   m.GetString(),
					sc:    m.GetString(),
					size:  int64(m.Get64()),
					mtime: time.Unix(0, int64(m.Get64())),
					isDir: m.GetBool(),
				})
			}
			m.SetBytes(batchBuff[batchLen : batchLen+4])
			batchLen = m.Get32()
		}
		return nil
	})
	return objs, isTruncated, nextMarker, err
}

func (c *PluginClient) ListAll(prefix string, marker string, followLink bool) (<-chan Object, error) {
	return nil, utils.ENOTSUP
}

func (c *PluginClient) ListUploads(marker string) ([]*PendingPart, string, error) {
	var parts []*PendingPart
	var nextMarker string
	err := c.call(func(conn net.Conn) (err error) {
		bodyLen := 2 + len(marker)
		buff := c.pool.Get(msg.HeaderLen + bodyLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bodyLen, msg.CmdListMPU)
		m.PutString(marker)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write ListUploads request")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdListMPU)
		if err != nil {
			return err
		}
		defer c.pool.Put(m.Bytes())
		nextMarker = m.GetString()
		batchLen := m.Get32()

		batchBuff := c.pool.Get(1<<20 + 4)
		defer c.pool.Put(batchBuff)
		for {
			if batchLen == 0 {
				break
			}
			if _, err = io.ReadFull(conn, batchBuff[:batchLen+4]); err != nil {
				return errors.Wrapf(err, "cmd %s failed to read response batch", msg.Cmd2Name[msg.CmdListMPU])
			}
			m.SetBytes(batchBuff[:batchLen])
			for m.HasMore() {
				parts = append(parts, &PendingPart{
					Key:      m.GetString(),
					UploadID: m.GetString(),
					Created:  time.Unix(0, int64(m.Get64())),
				})
			}
			m.SetBytes(batchBuff[batchLen : batchLen+4])
			batchLen = m.Get32()
		}
		return nil
	})
	return parts, nextMarker, err
}

func (c *PluginClient) Put(key string, in io.Reader, getters ...AttrGetter) error {
	return c.call(func(conn net.Conn) error {
		l, ok := in.(msg.Lener)
		if !ok {
			return errors.New("input reader does not implement Len() interface")
		}

		bLen := 2 + len(key) + 4 // 4 for length of data
		buff := c.pool.Get(msg.HeaderLen + bLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bLen, msg.CmdPut)
		m.PutString(key)
		m.Put32(uint32(l.Len()))

		var err error
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write Put header")
		}
		if _, err = io.Copy(conn, in); err != nil {
			return errors.Wrap(err, "failed to write Put body")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdPut)
		if m != nil {
			rid, sc := m.GetString(), m.GetString()
			attrs := ApplyGetters(getters...)
			attrs.SetRequestID(rid).SetStorageClass(sc)
			c.pool.Put(m.Bytes())
		}
		return err
	})
}

func (c *PluginClient) String() string {
	str := ""
	if err := c.call(func(conn net.Conn) (err error) {
		buff := c.pool.Get(msg.HeaderLen)
		m := msg.NewEncMsg(buff, 0, msg.CmdStr)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write String request")
		}

		m, err = c.readResp(conn, buff, msg.CmdStr)
		if m != nil {
			str = m.GetString()
			c.pool.Put(m.Bytes())
		}
		return err
	}); err != nil {
		c.Logger.Errorf("failed to call client.String: %v", err)
	}
	return str
}

func (c *PluginClient) UploadPart(key string, uploadID string, num int, body []byte) (*Part, error) {
	var part *Part
	err := c.call(func(conn net.Conn) (err error) {
		bLen := 2 + len(key) + 2 + len(uploadID) + 4 + 4
		buff := c.pool.Get(msg.HeaderLen + bLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bLen, msg.CmdUploadPart)
		m.PutString(key)
		m.PutString(uploadID)
		m.Put32(uint32(num))
		m.Put32(uint32(len(body)))

		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write UploadPart request")
		}
		if _, err = conn.Write(body); err != nil {
			return errors.Wrap(err, "failed to write UploadPart body")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdUploadPart)
		if err != nil {
			return err
		}
		defer c.pool.Put(m.Bytes())
		part = &Part{
			Num:  int(m.Get32()),
			Size: int(m.Get32()),
			ETag: m.GetString(),
		}
		return nil
	})
	return part, err
}

func (c *PluginClient) UploadPartCopy(key string, uploadID string, num int, srcKey string, off int64, size int64) (*Part, error) {
	var part *Part
	err := c.call(func(conn net.Conn) (err error) {
		bLen := 2 + len(key) + 2 + len(uploadID) + 4 + 2 + len(srcKey) + 8 + 8
		buff := c.pool.Get(msg.HeaderLen + bLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bLen, msg.CmdUploadPartCopy)
		m.PutString(key)
		m.PutString(uploadID)
		m.Put32(uint32(num))
		m.PutString(srcKey)
		m.Put64(uint64(off))
		m.Put64(uint64(size))

		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write UploadPartCopy request")
		}

		m, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdUploadPartCopy)
		if err != nil {
			return err
		}
		defer c.pool.Put(m.Bytes())
		part = &Part{
			Num:  int(m.Get32()),
			Size: int(m.Get32()),
			ETag: m.GetString(),
		}
		return nil
	})
	return part, err
}

func (c *PluginClient) Init(endpoint, accesskey, secretkey, token string) error {
	return c.call(func(conn net.Conn) (err error) {
		bodyLen := 8 + len(endpoint) + len(accesskey) + len(secretkey) + len(token)
		buff := c.pool.Get(msg.HeaderLen + bodyLen)
		defer c.pool.Put(buff)
		m := msg.NewEncMsg(buff, bodyLen, msg.CmdInit)
		m.PutString(endpoint)
		m.PutString(accesskey)
		m.PutString(secretkey)
		m.PutString(token)
		if _, err = conn.Write(m.Bytes()); err != nil {
			return errors.Wrap(err, "failed to write String request")
		}
		_, err = c.readResp(conn, buff[:msg.HeaderLen], msg.CmdInit)
		return err
	})
}

var pluginURL = "JFS_PLUGIN_URL"

func newPlugin(endpoint, accessKey, secretKey, token string) (ObjectStorage, error) {
	url := os.Getenv(pluginURL)
	cli, err := NewPluginClient(&PluginOptions{
		Version: version.Version(),
		MaxConn: 100,
		URL:     url,
		Logger:  logger,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create plugin client with URL %s", url)
	}
	err = cli.Init(endpoint, accessKey, secretKey, token)
	return cli, err
}

func init() {
	Register("plugin", newPlugin)
}
