package sony

import "testing"

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<NonRealTimeMeta xmlns="urn:schemas-professionalDisc:nonRealTimeMeta:ver.2.20" lastUpdate="2026-07-12T10:11:12+02:00">
  <Duration value="1440"/>
  <LtcChangeTable tcFps="24" halfStep="false">
    <LtcChange frameCount="0" value="01351600" status="increment"/>
    <LtcChange frameCount="1439" value="00362600" status="end"/>
  </LtcChangeTable>
  <CreationDate value="2026-07-12T10:10:12+02:00"/>
  <VideoFormat>
    <VideoRecPort port="DIRECT"/>
    <VideoFrame videoCodec="XOCN_ST" captureFps="23.98p" formatFps="23.98p"/>
    <VideoLayout pixel="4096" numOfVerticalLine="2160" aspectRatio="17:9"/>
  </VideoFormat>
  <AudioFormat numOfChannel="2">
    <AudioRecPort port="CH1" audioCodec="LPCM24" trackDst="CH1"/>
  </AudioFormat>
  <Device manufacturer="Sony" modelName="MPC-3628" serialNo="00012345"/>
  <Lens modelName="Zeiss Supreme 50"/>
  <AcquisitionRecord>
    <Group name="CameraUnitMetadataSet">
      <Item name="CaptureGammaEquation" value="s-log3-cine"/>
      <Item name="CaptureColorPrimaries" value="s-gamut3-cine"/>
      <Item name="ISOSensitivity" value="800"/>
      <Item name="LightingPreset" value="5600K"/>
      <Item name="ShutterSpeed_Angle" value="180"/>
      <Item name="NeutralDensityFilterWheelSetting" value="1/4"/>
    </Group>
    <Group name="LensUnitMetadataSet">
      <Item name="FocalLength" value="50"/>
      <Item name="IrisFNumber" value="2.8"/>
    </Group>
  </AcquisitionRecord>
</NonRealTimeMeta>`

func TestParseAndMap(t *testing.T) {
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	f := d.Fields()
	if *f.CameraMake != "Sony" || *f.CameraModel != "MPC-3628" || *f.CameraSerial != "00012345" || *f.Lens != "Zeiss Supreme 50" {
		t.Errorf("device wrong: %v", f.Set())
	}
	if *f.Codec != "XOCN_ST" || *f.Width != 4096 || *f.Height != 2160 || *f.AudioChannels != 2 || *f.AudioBitDepth != 24 {
		t.Errorf("format wrong: %v", f.Set())
	}
	if fps := *f.FPS; fps < 23.97 || fps > 23.98 {
		t.Errorf("fps = %v", fps)
	}
	if *f.TCStart != "00:16:35:01" || *f.TCEnd != "00:26:36:00" || *f.FrameCount != 1440 || *f.DurationS != 60 {
		t.Errorf("timecode wrong: %s %s %d %v", *f.TCStart, *f.TCEnd, *f.FrameCount, *f.DurationS)
	}
	if *f.ColorGamma != "s-gamut3-cine / s-log3-cine" || *f.ISO != 800 || *f.WBKelvin != 5600 || *f.ShutterAngle != 180 || *f.ND != "1/4" {
		t.Errorf("exposure wrong: %v", f.Set())
	}
	if *f.FocalMM != 50 || *f.TStop != 2.8 {
		t.Errorf("lens wrong: %v %v", *f.FocalMM, *f.TStop)
	}
	if f.RecordedAt == nil || f.RecordedAt.Format("2006-01-02T15:04:05Z") != "2026-07-12T08:10:12Z" {
		t.Errorf("recorded_at = %v", f.RecordedAt)
	}
}

func TestDecodeLTC(t *testing.T) {
	if got := DecodeLTC("01351600"); got != "00:16:35:01" {
		t.Errorf("DecodeLTC = %s", got)
	}
}
