package params

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/livekit/protocol/egress"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"

	"github.com/livekit/livekit-egress/pkg/config"
	"github.com/livekit/livekit-egress/pkg/errors"
)

type Params struct {
	SourceParams
	AudioParams
	VideoParams

	// format
	IsStream       bool
	StreamProtocol livekit.StreamProtocol
	StreamUrls     []string
	FileParams

	// info
	Info       *livekit.EgressInfo
	FileInfo   *livekit.FileInfo
	StreamInfo map[string]*livekit.StreamInfo

	EgressWebSocketURL string

	// logger
	Logger logger.Logger
}

type SourceParams struct {
	// source
	RoomName     string
	Token        string
	LKUrl        string
	TemplateBase string

	// web source
	IsWebInput     bool
	Display        string
	Layout         string
	CustomBase     string
	CustomInputURL string

	// sdk source
	TrackID      string
	AudioTrackID string
	VideoTrackID string
}

type AudioParams struct {
	AudioEnabled   bool
	AudioCodec     livekit.AudioCodec
	AudioBitrate   int32
	AudioFrequency int32
}

type VideoParams struct {
	VideoEnabled bool
	VideoCodec   livekit.VideoCodec
	Width        int32
	Height       int32
	Depth        int32
	Framerate    int32
	VideoBitrate int32
}

type FileParams struct {
	Filename   string
	Filepath   string
	FileType   livekit.EncodedFileType
	FileUpload interface{}
}

func GetPipelineParams(conf *config.Config, request *livekit.StartEgressRequest) (*Params, error) {
	params := getEncodingParams(request)
	params.Info = &livekit.EgressInfo{
		EgressId: request.EgressId,
		RoomId:   request.RoomId,
		Status:   livekit.EgressStatus_EGRESS_STARTING,
	}
	params.Logger = logger.Logger(logger.GetLogger().WithValues("egressID", request.EgressId))

	var format string
	switch req := request.Request.(type) {
	case *livekit.StartEgressRequest_RoomComposite:
		params.Info.Request = &livekit.EgressInfo_RoomComposite{RoomComposite: req.RoomComposite}

		params.IsWebInput = true
		params.Display = fmt.Sprintf(":%d", 10+rand.Intn(2147483637))
		params.AudioEnabled = !req.RoomComposite.VideoOnly
		params.VideoEnabled = !req.RoomComposite.AudioOnly
		params.RoomName = req.RoomComposite.RoomName
		params.Layout = req.RoomComposite.Layout
		if req.RoomComposite.CustomBaseUrl != "" {
			params.TemplateBase = req.RoomComposite.CustomBaseUrl
		} else {
			params.TemplateBase = conf.TemplateBase
		}

		switch o := req.RoomComposite.Output.(type) {
		case *livekit.RoomCompositeEgressRequest_File:
			format = o.File.FileType.String()
			if err := params.updateFileInfo(conf, o.File.FileType, o.File.Filepath, o.File.Output); err != nil {
				return nil, err
			}
		case *livekit.RoomCompositeEgressRequest_Stream:
			format = o.Stream.Protocol.String()
			if err := params.updateStreamInfo(o.Stream.Protocol, o.Stream.Urls); err != nil {
				return nil, err
			}
		default:
			return nil, errors.ErrInvalidInput("output")
		}

	case *livekit.StartEgressRequest_TrackComposite:
		params.Info.Request = &livekit.EgressInfo_TrackComposite{TrackComposite: req.TrackComposite}

		params.AudioTrackID = req.TrackComposite.AudioTrackId
		params.AudioEnabled = params.AudioTrackID != ""
		params.VideoTrackID = req.TrackComposite.VideoTrackId
		params.VideoEnabled = params.VideoTrackID != ""
		params.RoomName = req.TrackComposite.RoomName

		switch o := req.TrackComposite.Output.(type) {
		case *livekit.TrackCompositeEgressRequest_File:
			format = o.File.FileType.String()
			if err := params.updateFileInfo(conf, o.File.FileType, o.File.Filepath, o.File.Output); err != nil {
				return nil, err
			}
		case *livekit.TrackCompositeEgressRequest_Stream:
			format = o.Stream.Protocol.String()
			if err := params.updateStreamInfo(o.Stream.Protocol, o.Stream.Urls); err != nil {
				return nil, err
			}
		default:
			return nil, errors.ErrInvalidInput("output")
		}

	case *livekit.StartEgressRequest_Track:
		params.Info.Request = &livekit.EgressInfo_Track{Track: req.Track}

		params.RoomName = req.Track.RoomName
		params.TrackID = req.Track.TrackId

		switch o := req.Track.Output.(type) {
		case *livekit.TrackEgressRequest_WebsocketUrl:
			params.EgressWebSocketURL = o.WebsocketUrl
		default:
			return nil, errors.ErrInvalidInput("output")
		}

		//return nil, errors.ErrNotSupported("track requests")
		// return params, nil
	default:
		return nil, errors.ErrInvalidInput("request")
	}

	// token
	if request.Token != "" {
		params.Token = request.Token
	} else if conf.ApiKey != "" && conf.ApiSecret != "" {
		token, err := egress.BuildEgressToken(params.Info.EgressId, conf.ApiKey, conf.ApiSecret, params.RoomName)
		if err != nil {
			return nil, err
		}
		params.Token = token
	} else {
		return nil, errors.ErrInvalidInput("token or api key/secret")
	}

	if request.WsUrl != "" {
		params.LKUrl = request.WsUrl
	} else if conf.WsUrl != "" {
		params.LKUrl = conf.WsUrl
	} else {
		return nil, errors.ErrInvalidInput("ws_url")
	}

	// check audio codec
	if params.AudioEnabled {
		if params.AudioCodec == livekit.AudioCodec_DEFAULT_AC {
			params.AudioCodec = DefaultAudioCodecs[format]
		} else if !compatibleAudioCodecs[format][params.AudioCodec] {
			return nil, errors.ErrIncompatible(format, params.AudioCodec)
		}
	}

	// check video codec
	if params.VideoEnabled {
		if params.VideoCodec == livekit.VideoCodec_DEFAULT_VC {
			params.VideoCodec = DefaultVideoCodecs[format]
		} else if !compatibleVideoCodecs[format][params.VideoCodec] {
			return nil, errors.ErrIncompatible(format, params.VideoCodec)
		}
	}

	return params, nil
}

func getEncodingParams(request *livekit.StartEgressRequest) *Params {
	var preset livekit.EncodingOptionsPreset = -1
	var advanced *livekit.EncodingOptions

	switch req := request.Request.(type) {
	case *livekit.StartEgressRequest_RoomComposite:
		switch opts := req.RoomComposite.Options.(type) {
		case *livekit.RoomCompositeEgressRequest_Preset:
			preset = opts.Preset
		case *livekit.RoomCompositeEgressRequest_Advanced:
			advanced = opts.Advanced
		}
	case *livekit.StartEgressRequest_TrackComposite:
		switch options := req.TrackComposite.Options.(type) {
		case *livekit.TrackCompositeEgressRequest_Preset:
			preset = options.Preset
		case *livekit.TrackCompositeEgressRequest_Advanced:
			advanced = options.Advanced
		}
	}

	params := fullHD30
	if preset != -1 {
		switch preset {
		case livekit.EncodingOptionsPreset_H264_720P_30:
			params = hd30
		case livekit.EncodingOptionsPreset_H264_720P_60:
			params = hd60
		case livekit.EncodingOptionsPreset_H264_1080P_30:
			// default
		case livekit.EncodingOptionsPreset_H264_1080P_60:
			params = fullHD60
		}
	} else if advanced != nil {
		// audio
		params.AudioCodec = advanced.AudioCodec
		if advanced.AudioBitrate != 0 {
			params.AudioBitrate = advanced.AudioBitrate
		}
		if advanced.AudioFrequency != 0 {
			params.AudioFrequency = advanced.AudioFrequency
		}

		// video
		params.VideoCodec = advanced.VideoCodec
		if advanced.Width != 0 {
			params.Width = advanced.Width
		}
		if advanced.Height != 0 {
			params.Height = advanced.Height
		}
		if advanced.Depth != 0 {
			params.Depth = advanced.Depth
		}
		if advanced.Framerate != 0 {
			params.Framerate = advanced.Framerate
		}
		if advanced.VideoBitrate != 0 {
			params.VideoBitrate = advanced.VideoBitrate
		}
	}

	return &params
}

func (p *Params) updateStreamInfo(protocol livekit.StreamProtocol, urls []string) error {
	p.IsStream = true
	p.StreamProtocol = protocol
	p.StreamUrls = urls

	p.StreamInfo = make(map[string]*livekit.StreamInfo)
	var streamInfoList []*livekit.StreamInfo
	for _, url := range urls {
		switch protocol {
		case livekit.StreamProtocol_RTMP:
			if !strings.HasPrefix(url, "rtmp://") && !strings.HasPrefix(url, "rtmps://") {
				return errors.ErrInvalidUrl(url, protocol)
			}
		}

		info := &livekit.StreamInfo{
			Url: url,
		}
		p.StreamInfo[url] = info
		streamInfoList = append(streamInfoList, info)
	}
	p.Info.Result = &livekit.EgressInfo_Stream{Stream: &livekit.StreamInfoList{Info: streamInfoList}}
	return nil
}

func (p *Params) updateFileInfo(conf *config.Config, fileType livekit.EncodedFileType, filepath string, output interface{}) error {
	local := false
	switch o := output.(type) {
	case *livekit.EncodedFileOutput_S3:
		p.FileUpload = o.S3
	case *livekit.EncodedFileOutput_Azure:
		p.FileUpload = o.Azure
	case *livekit.EncodedFileOutput_Gcp:
		p.FileUpload = o.Gcp
	default:
		if conf.FileUpload != nil {
			p.FileUpload = conf.FileUpload
		} else {
			local = true
		}
	}

	filename, err := getFilename(filepath, fileType, p.RoomName, local)
	if err != nil {
		return err
	}

	p.Filename = filename
	p.Filepath = filepath
	p.FileType = fileType
	p.FileInfo = &livekit.FileInfo{Filename: filename}
	p.Info.Result = &livekit.EgressInfo_File{File: p.FileInfo}

	return nil
}

func getFilename(filepath string, fileType livekit.EncodedFileType, roomName string, local bool) (string, error) {
	ext := "." + strings.ToLower(fileType.String())
	if filepath == "" {
		return fmt.Sprintf("%s-%v%s", roomName, time.Now().String(), ext), nil
	}

	// check for extension
	if !strings.HasSuffix(filepath, ext) {
		filepath = filepath + ext
	}

	// get filename from path
	idx := strings.LastIndex(filepath, "/")
	if idx == -1 {
		return filepath, nil
	}

	if local {
		if err := os.MkdirAll(filepath[:idx], os.ModeDir); err != nil {
			return "", err
		}
		return filepath, nil
	}

	return filepath[idx+1:], nil
}
