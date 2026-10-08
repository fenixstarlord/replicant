// Package bwf reads Broadcast Wave metadata: the fmt chunk, the bext
// chunk (EBU Tech 3285), and the iXML chunk that production recorders
// write (scene, take, tape, circled, notes, timecode rate, track names).
package bwf

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/meta"
)

// Extractor parses WAV headers directly; no external tool.
type Extractor struct{}

func (Extractor) Name() string                             { return "bwf" }
func (Extractor) Priority() int                            { return extract.PriorityVendor }
func (Extractor) Available(context.Context) (bool, string) { return true, "builtin" }

func (Extractor) Matches(c *clips.Clip) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(c.PrimaryFile()), "."))
	return ext == "wav" || ext == "bwf"
}

func (Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(c.PrimaryFile())))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := Parse(f)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(info)
	return &meta.Result{Fields: info.Fields(), Raw: raw}, nil
}

// Info is everything read from the header chunks.
type Info struct {
	Fmt      Fmt    `json:"fmt"`
	DataSize int64  `json:"data_size"`
	Bext     *Bext  `json:"bext,omitempty"`
	IXML     *IXML  `json:"ixml,omitempty"`
	IXMLText string `json:"ixml_xml,omitempty"`
}

// Fmt is the PCM format chunk.
type Fmt struct {
	Format        uint16 `json:"format"`
	Channels      uint16 `json:"channels"`
	SampleRate    uint32 `json:"sample_rate"`
	BitsPerSample uint16 `json:"bits_per_sample"`
}

// Bext is the broadcast extension chunk.
type Bext struct {
	Description         string `json:"description"`
	Originator          string `json:"originator"`
	OriginatorReference string `json:"originator_reference"`
	OriginationDate     string `json:"origination_date"`
	OriginationTime     string `json:"origination_time"`
	TimeReference       uint64 `json:"time_reference"`
	Version             uint16 `json:"version"`
	UMID                string `json:"umid,omitempty"`
	CodingHistory       string `json:"coding_history,omitempty"`
}

// IXML is the subset of the iXML chunk we map.
type IXML struct {
	XMLName  xml.Name `xml:"BWFXML" json:"-"`
	Version  string   `xml:"IXML_VERSION" json:"version,omitempty"`
	Project  string   `xml:"PROJECT" json:"project,omitempty"`
	Scene    string   `xml:"SCENE" json:"scene,omitempty"`
	Take     string   `xml:"TAKE" json:"take,omitempty"`
	Tape     string   `xml:"TAPE" json:"tape,omitempty"`
	Circled  string   `xml:"CIRCLED" json:"circled,omitempty"`
	Note     string   `xml:"NOTE" json:"note,omitempty"`
	FileUID  string   `xml:"FILE_UID" json:"file_uid,omitempty"`
	Speed    Speed    `xml:"SPEED" json:"speed"`
	Tracks   []Track  `xml:"TRACK_LIST>TRACK" json:"tracks,omitempty"`
	BextDesc string   `xml:"BEXT>BWF_DESCRIPTION" json:"-"`
}

// Speed holds the iXML timecode and sample-rate block.
type Speed struct {
	TimecodeRate   string `xml:"TIMECODE_RATE" json:"timecode_rate,omitempty"`
	TimecodeFlag   string `xml:"TIMECODE_FLAG" json:"timecode_flag,omitempty"`
	FileSampleRate string `xml:"FILE_SAMPLE_RATE" json:"file_sample_rate,omitempty"`
	AudioBitDepth  string `xml:"AUDIO_BIT_DEPTH" json:"audio_bit_depth,omitempty"`
	MasterSpeed    string `xml:"MASTER_SPEED" json:"master_speed,omitempty"`
	CurrentSpeed   string `xml:"CURRENT_SPEED" json:"current_speed,omitempty"`
}

// Track is one iXML track entry.
type Track struct {
	ChannelIndex    string `xml:"CHANNEL_INDEX" json:"channel_index,omitempty"`
	InterleaveIndex string `xml:"INTERLEAVE_INDEX" json:"interleave_index,omitempty"`
	Name            string `xml:"NAME" json:"name,omitempty"`
	Function        string `xml:"FUNCTION" json:"function,omitempty"`
}

// Parse reads the RIFF/WAVE chunks before the audio data.
func Parse(r io.ReadSeeker) (*Info, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var hdr [12]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, err
	}
	if string(hdr[:4]) != "RIFF" && string(hdr[:4]) != "RF64" {
		return nil, errors.New("not a RIFF file")
	}
	if string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("not a WAVE file")
	}
	info := &Info{}
	var ds64Data uint64
	for {
		var ch [8]byte
		if _, err := io.ReadFull(br, ch[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		id := string(ch[:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		if id == "data" {
			if size == int64(^uint32(0)) && ds64Data > 0 {
				size = int64(ds64Data)
			}
			info.DataSize = size
			break // audio samples follow; nothing more to read
		}
		if size > 16<<20 {
			return nil, fmt.Errorf("chunk %q too large (%d bytes)", id, size)
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, fmt.Errorf("chunk %q: %w", id, err)
		}
		if size&1 == 1 {
			br.ReadByte() //nolint:errcheck // pad byte
		}
		switch id {
		case "fmt ":
			if len(body) >= 16 {
				info.Fmt = Fmt{
					Format:        binary.LittleEndian.Uint16(body[0:2]),
					Channels:      binary.LittleEndian.Uint16(body[2:4]),
					SampleRate:    binary.LittleEndian.Uint32(body[4:8]),
					BitsPerSample: binary.LittleEndian.Uint16(body[14:16]),
				}
			}
		case "ds64":
			if len(body) >= 16 {
				ds64Data = binary.LittleEndian.Uint64(body[8:16])
			}
		case "bext":
			info.Bext = parseBext(body)
		case "iXML":
			text := strings.TrimRight(string(body), "\x00")
			info.IXMLText = text
			var ix IXML
			if err := xml.Unmarshal([]byte(text), &ix); err == nil {
				info.IXML = &ix
			}
		}
	}
	if info.Fmt.SampleRate == 0 {
		return nil, errors.New("no fmt chunk")
	}
	return info, nil
}

func parseBext(b []byte) *Bext {
	if len(b) < 602 {
		return nil
	}
	cstr := func(x []byte) string { return strings.TrimRight(strings.TrimRight(string(x), "\x00"), " ") }
	bx := &Bext{
		Description:         cstr(b[0:256]),
		Originator:          cstr(b[256:288]),
		OriginatorReference: cstr(b[288:320]),
		OriginationDate:     cstr(b[320:330]),
		OriginationTime:     cstr(b[330:338]),
		TimeReference:       uint64(binary.LittleEndian.Uint32(b[338:342])) | uint64(binary.LittleEndian.Uint32(b[342:346]))<<32,
		Version:             binary.LittleEndian.Uint16(b[346:348]),
	}
	if umid := b[348:412]; strings.Trim(string(umid), "\x00") != "" {
		bx.UMID = hex.EncodeToString(umid)
	}
	if len(b) > 602 {
		bx.CodingHistory = cstr(b[602:])
	}
	return bx
}

var sdLine = regexp.MustCompile(`(?m)^s([A-Z0-9]+)=(.*?)\r?$`)

// descriptionKV parses Sound Devices style "sSCENE=36A" lines from the
// bext description, used when there is no iXML.
func descriptionKV(desc string) map[string]string {
	out := map[string]string{}
	for _, m := range sdLine.FindAllStringSubmatch(desc, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// Fields maps the parsed header to normalized metadata.
func (in *Info) Fields() meta.Fields {
	var f meta.Fields
	f.Container = meta.Str("wav")
	f.Codec = meta.Str(fmt.Sprintf("pcm_%d", in.Fmt.BitsPerSample))
	if in.Fmt.Channels > 0 {
		f.AudioChannels = meta.Int(int64(in.Fmt.Channels))
	}
	f.SampleRate = meta.Int(int64(in.Fmt.SampleRate))
	if in.Fmt.BitsPerSample > 0 {
		f.AudioBitDepth = meta.Int(int64(in.Fmt.BitsPerSample))
	}
	bytesPerFrame := int64(in.Fmt.Channels) * int64(in.Fmt.BitsPerSample) / 8
	if in.DataSize > 0 && bytesPerFrame > 0 {
		f.DurationS = meta.Float(float64(in.DataSize/bytesPerFrame) / float64(in.Fmt.SampleRate))
	}

	kv := map[string]string{}
	if in.Bext != nil {
		kv = descriptionKV(in.Bext.Description)
		if t := parseDateTime(in.Bext.OriginationDate, in.Bext.OriginationTime); t != nil {
			f.RecordedAt = t
		}
	}
	var fps float64
	if in.IXML != nil {
		fps = parseRate(in.IXML.Speed.TimecodeRate)
		f.Scene = meta.Str(in.IXML.Scene)
		f.Take = meta.Str(in.IXML.Take)
		f.Reel = meta.Str(in.IXML.Tape)
		if in.IXML.Circled != "" {
			f.Circled = meta.Bool(strings.EqualFold(in.IXML.Circled, "TRUE"))
		}
	}
	if fps == 0 {
		fps = parseSDSpeed(kv["SPEED"])
	}
	if f.Scene == nil {
		f.Scene = meta.Str(kv["SCENE"])
	}
	if f.Take == nil {
		f.Take = meta.Str(kv["TAKE"])
	}
	if f.Reel == nil {
		f.Reel = meta.Str(kv["TAPE"])
	}
	if f.Circled == nil && kv["CIRCLED"] != "" {
		f.Circled = meta.Bool(strings.EqualFold(kv["CIRCLED"], "TRUE"))
	}
	if fps > 0 {
		f.FPS = meta.Float(fps)
		if in.Bext != nil && in.Fmt.SampleRate > 0 {
			f.TCStart = meta.Str(SamplesToTimecode(in.Bext.TimeReference, int64(in.Fmt.SampleRate), fps))
			if f.DurationS != nil {
				frames := int64(*f.DurationS*fps + 0.5)
				f.FrameCount = meta.Int(frames)
				end := in.Bext.TimeReference + uint64(in.DataSize/bytesPerFrame)
				f.TCEnd = meta.Str(SamplesToTimecode(end, int64(in.Fmt.SampleRate), fps))
			}
		}
	}
	return f
}

// SamplesToTimecode converts a sample count since midnight to HH:MM:SS:FF.
func SamplesToTimecode(samples uint64, sampleRate int64, fps float64) string {
	secs := int64(samples / uint64(sampleRate))
	rem := samples % uint64(sampleRate)
	frames := int64(float64(rem) * fps / float64(sampleRate))
	secs %= 24 * 3600
	return fmt.Sprintf("%02d:%02d:%02d:%02d", secs/3600, (secs/60)%60, secs%60, frames)
}

func parseRate(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	if !ok {
		v, _ := strconv.ParseFloat(r, 64)
		return v
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}

// parseSDSpeed reads Sound Devices "024.000-ND" / "029.970-DF".
func parseSDSpeed(s string) float64 {
	s, _, _ = strings.Cut(s, "-")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseDateTime(d, t string) *time.Time {
	if d == "" {
		return nil
	}
	d = strings.NewReplacer("-", "", ":", "", "_", "", "/", "").Replace(d)
	t = strings.NewReplacer("-", "", ":", "", "_", "", ".", "").Replace(t)
	for _, layout := range []string{"20060102 150405", "20060102 1504", "20060102 "} {
		if ts, err := time.Parse(layout, d+" "+t); err == nil {
			return meta.Time(ts)
		}
	}
	return nil
}
