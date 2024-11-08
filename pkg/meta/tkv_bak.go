package meta

import "io"

func (m *kvMeta) newCounterDS(bak *BakFormat) iDumpedSeg {
	panic("implement me")
}

func (m *kvMeta) BuildDumpedSeg(typ int, opt *DumpOption) iDumpedSeg {
	panic("implement me")
}

func (m *kvMeta) BuildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	panic("implement me")
}

func (m *kvMeta) DumpMetaV2(ctx Context, w io.Writer, opt *DumpOption) (err error) {
	return nil
}

func (m *kvMeta) LoadMetaV2(ctx Context, r io.Reader, opt *LoadOption) error {
	return nil
}
