package minhq

import (
	"bytes"
	"io"
	"io/ioutil"

	"github.com/martinthomson/minhq/hc"
)

type settingType uint16

const (
	settingTableSize         = settingType(1)
	settingMaxHeaderListSize = settingType(6) // TODO implement
)

type settingsWriter struct {
	config *Config
}

// WriteTo writes out just one integer setting for the moment.
func (sw *settingsWriter) WriteTo(w io.Writer) (int64, error) {
	fw := NewFrameWriter(w)
	return sw.writeIntSetting(fw, settingTableSize,
		uint64(sw.config.DecoderTableCapacity))
}

func (sw *settingsWriter) writeIntSetting(fw FrameWriter, s settingType, v uint64) (int64, error) {
	var buf bytes.Buffer
	tmpfw := NewFrameWriter(&buf)
	_, err := tmpfw.WriteVarint(v)
	if err != nil {
		return 0, err
	}

	err = fw.WriteBits(uint64(s), 16)
	n, err := fw.WriteVarint(uint64(buf.Len()))
	written := int64(n) + 2
	if err != nil {
		return written, err
	}
	n64, err := io.Copy(fw, &buf)
	written += n64
	return written, err
}

type settingsReader struct {
	c *connection
}

func (sr *settingsReader) readSettings(r FrameReader) error {
	for {
		s, err := r.ReadBits(16)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		len, err := r.ReadVarint()
		if err != nil {
			return err
		}
		lr := r.Limited(len)
		switch settingType(s) {
		case settingTableSize:
			n, err := lr.ReadVarint()
			if err != nil {
				return err
			}
			sr.c.encoder.SetCapacity(hc.TableCapacity(n))
		default:
			_, err = io.Copy(ioutil.Discard, lr)
		}
		if err != nil {
			return err
		}
	}
}
