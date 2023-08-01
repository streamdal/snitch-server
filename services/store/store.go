package store

import (
	"context"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/streamdal/natty"
	"github.com/streamdal/snitch-protos/build/go/protos"
	"google.golang.org/protobuf/proto"

	"github.com/streamdal/snitch-server/util"
)

/*

!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!

Storage strategy is defined here:

https://www.notion.so/streamdal/Snitch-Server-Storage-Spec-417bfa71f04b481082373ad18cbdb0e9

!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!

`store` is a service that handles storage and retrieval of data such as service
registrations and service commands.

`store` is backed by a `natty.INatty` instance, which is a wrapper for NATS.

All reads, writes and deletes are performed via NATS -- snitch-server does NOT
store any persistent state in memory!
*/

var (
	ErrPipelineNotFound = errors.New("pipeline not found")
	ErrRegisterNotFound = errors.New("no registration found")
)

type IStore interface {
	AddRegistration(ctx context.Context, req *protos.RegisterRequest) error
	DeleteRegistration(ctx context.Context, req *protos.DeregisterRequest) error
	AddHeartbeat(ctx context.Context, req *protos.HeartbeatRequest) error
	GetPipelines(ctx context.Context) (map[string]*protos.Pipeline, error)
	GetPipeline(ctx context.Context, pipelineId string) (*protos.Pipeline, error)
	CreatePipeline(ctx context.Context, pipeline *protos.Pipeline) error
	AddAudience(ctx context.Context, req *protos.NewAudienceRequest) error
	DeletePipeline(ctx context.Context, pipelineId string) error
	UpdatePipeline(ctx context.Context, pipeline *protos.Pipeline) error
	AttachPipeline(ctx context.Context, req *protos.AttachPipelineRequest) error
	DetachPipeline(ctx context.Context, req *protos.DetachPipelineRequest) error
	PausePipeline(ctx context.Context, req *protos.PausePipelineRequest) error
	ResumePipeline(ctx context.Context, req *protos.ResumePipelineRequest) error
}

type Options struct {
	NATSBackend natty.INatty
	ShutdownCtx context.Context
	NodeName    string
}

type Store struct {
	options *Options
	log     *logrus.Entry
}

func New(opts *Options) (*Store, error) {
	if err := opts.validate(); err != nil {
		return nil, errors.Wrap(err, "error validating options")
	}

	return &Store{
		options: opts,
		log:     logrus.WithField("pkg", "store"),
	}, nil
}

// DONE
func (s *Store) AddRegistration(ctx context.Context, req *protos.RegisterRequest) error {
	llog := s.log.WithField("method", "AddRegistration")
	llog.Debug("received request to add registration")

	// Add registration in snitch_live bucket
	if err := s.options.NATSBackend.Put(
		ctx,
		NATSLiveBucket,
		NATSLiveKey(req.SessionId, s.options.NodeName, "register"),
		nil,
		NATSLiveTTL,
	); err != nil {
		return errors.Wrap(err, "error adding registration to K/V")
	}

	// Save audience(s) if they are present in Register request
	if req.Audiences != nil && len(req.Audiences) > 0 {
		for _, audience := range req.Audiences {
			llog.Debugf("adding audience '%s' for session '%s'", audience, req.SessionId)

			if err := s.AddAudience(ctx, &protos.NewAudienceRequest{
				SessionId: req.SessionId,
				Audience:  audience,
			}); err != nil {
				return errors.Wrap(err, "error adding audience")
			}
		}
	}

	return nil
}

// DONE
func (s *Store) DeleteRegistration(ctx context.Context, req *protos.DeregisterRequest) error {
	llog := s.log.WithField("method", "DeleteRegistration")
	llog.Debug("received request to delete registration")

	// Remove from K/V
	if err := s.options.NATSBackend.Delete(
		ctx,
		NATSLiveBucket,
		NATSLiveKey(req.SessionId, s.options.NodeName, "register"),
	); err != nil {
		return errors.Wrap(err, "error deleting registration from K/V")
	}

	return nil
}

// AddHeartbeat updates the TTL for a given registration
func (s *Store) AddHeartbeat(ctx context.Context, req *protos.HeartbeatRequest) error {
	llog := s.log.WithField("method", "AddHeartbeat")
	llog.Debug("received request to add heartbeat")

	// TODO: Implement

	return nil
}

// DONE
func (s *Store) GetPipelines(ctx context.Context) (map[string]*protos.Pipeline, error) {
	llog := s.log.WithField("method", "GetPipelines")
	llog.Debug("received request to get pipelines")

	pipelineIds, err := s.options.NATSBackend.Keys(ctx, NATSPipelineBucket)
	if err != nil {
		return nil, errors.Wrap(err, "error fetching pipeline keys from NATS")
	}

	// k == pipelineId
	pipelines := make(map[string]*protos.Pipeline, 0)

	for _, pipelineId := range pipelineIds {
		pipelineData, err := s.options.NATSBackend.Get(ctx, NATSPipelineBucket, pipelineId)
		if err != nil {
			return nil, errors.Wrapf(err, "error fetching pipeline '%s' from NATS", pipelineId)
		}

		pipeline := &protos.Pipeline{}

		if err := proto.Unmarshal(pipelineData, pipeline); err != nil {
			return nil, errors.Wrapf(err, "error unmarshaling pipeline '%s'", pipelineId)
		}

		pipelines[pipelineId] = pipeline
	}

	return pipelines, nil
}

// DONE
func (s *Store) GetPipeline(ctx context.Context, pipelineId string) (*protos.Pipeline, error) {
	llog := s.log.WithField("method", "GetPipeline")
	llog.Debug("received request to get pipeline")

	pipelineData, err := s.options.NATSBackend.Get(ctx, NATSPipelineBucket, pipelineId)
	if err != nil {
		if err != nats.ErrKeyNotFound {
			return nil, ErrPipelineNotFound
		}

		return nil, errors.Wrap(err, "error fetching pipeline from NATS")
	}

	pipeline := &protos.Pipeline{}

	if err := proto.Unmarshal(pipelineData, pipeline); err != nil {
		return nil, errors.Wrap(err, "error deserializing pipeline")
	}

	return pipeline, nil
}

// DONE
func (s *Store) CreatePipeline(ctx context.Context, pipeline *protos.Pipeline) error {
	llog := s.log.WithField("method", "CreatePipeline")
	llog.Debug("received request to create pipeline")

	// Save to K/V
	pipelineData, err := proto.Marshal(pipeline)
	if err != nil {
		return errors.Wrap(err, "error serializing pipeline to protobuf")
	}

	if err := s.options.NATSBackend.Put(ctx, NATSPipelineBucket, NATSPipelineKey(pipeline.Id), pipelineData); err != nil {
		return errors.Wrap(err, "error saving pipeline to NATS")
	}

	return nil
}

// DONE
func (s *Store) DeletePipeline(ctx context.Context, pipelineId string) error {
	llog := s.log.WithField("method", "DeletePipeline")
	llog.Debug("received request to delete pipeline")

	// Does this pipeline exist?
	if _, err := s.GetPipeline(ctx, pipelineId); err != nil {
		return errors.Wrap(err, "error fetching pipeline")
	}

	if err := s.options.NATSBackend.Delete(ctx, NATSPipelineBucket, NATSPipelineKey(pipelineId)); err != nil {
		return errors.Wrap(err, "error deleting pipeline from NATS")
	}

	return nil
}

// DONE
func (s *Store) UpdatePipeline(ctx context.Context, pipeline *protos.Pipeline) error {
	llog := s.log.WithField("method", "UpdatePipeline")
	llog.Debug("received request to update pipeline")

	// Save to K/V
	pipelineData, err := proto.Marshal(pipeline)
	if err != nil {
		return errors.Wrap(err, "error serializing pipeline to protobuf")
	}

	if err := s.options.NATSBackend.Put(ctx, NATSPipelineBucket, NATSPipelineKey(pipeline.Id), pipelineData); err != nil {
		return errors.Wrap(err, "error saving pipeline to NATS")
	}

	return nil
}

// DONE
func (s *Store) AttachPipeline(ctx context.Context, req *protos.AttachPipelineRequest) error {
	llog := s.log.WithField("method", "AttachPipeline")
	llog.Debug("received request to attach pipeline")

	// Does this pipeline exist?
	if _, err := s.GetPipeline(ctx, req.PipelineId); err != nil {
		return errors.Wrap(err, "error fetching pipeline")
	}

	// Store attachment in NATS
	natsKey := NATSConfigKey(util.AudienceStr(req.Audience))

	if err := s.options.NATSBackend.Put(ctx, NATSConfigBucket, natsKey, []byte(req.PipelineId)); err != nil {
		return errors.Wrap(err, "error saving pipeline attachment to NATS")
	}

	return nil
}

// DONE
func (s *Store) DetachPipeline(ctx context.Context, req *protos.DetachPipelineRequest) error {
	llog := s.log.WithField("method", "DetachPipeline")
	llog.Debug("received request to detach pipeline")

	// Does this pipeline exist?
	if _, err := s.GetPipeline(ctx, req.PipelineId); err != nil {
		if err == ErrPipelineNotFound {
			llog.Debugf("pipeline '%s' not found - nothing to do", req.PipelineId)
			return nil
		}

		return errors.Wrap(err, "error fetching pipeline")
	}

	if err := s.options.NATSBackend.Delete(ctx, NATSConfigBucket, NATSConfigKey(util.AudienceStr(req.Audience))); err != nil {
		return errors.Wrap(err, "error deleting pipeline attachment from NATS")
	}

	return nil
}

// DONE
func (s *Store) PausePipeline(ctx context.Context, req *protos.PausePipelineRequest) error {
	llog := s.log.WithField("method", "PausePipeline")
	llog.Debug("received request to pause pipeline")

	// Does this pipeline exist?
	if _, err := s.GetPipeline(ctx, req.PipelineId); err != nil {
		return errors.Wrap(err, "error fetching pipeline")
	}

	paused, err := s.IsPaused(ctx, req.Audience, req.PipelineId)
	if err != nil {
		return errors.Wrap(err, "error checking if pipeline is paused")
	}

	if paused {
		llog.Debugf("pipeline '%s' already paused; nothing to do", req.PipelineId)
		return nil
	}

	llog.Debugf("pipeline '%s' not paused; setting pause state now", req.PipelineId)

	if err := s.options.NATSBackend.Put(
		ctx,
		NATSPausedBucket,
		NATSPausedKey(util.AudienceStr(req.Audience), req.PipelineId),
		nil,
	); err != nil {
		return errors.Wrap(err, "error saving pipeline pause state")
	}

	return nil
}

// DONE
// IsPaused returns if pipeline is paused and if it exists
func (s *Store) IsPaused(ctx context.Context, audience *protos.Audience, pipelineID string) (bool, error) {
	llog := s.log.WithField("method", "IsPaused")
	llog.Debug("received request to check if pipeline is paused")

	if _, err := s.options.NATSBackend.Get(ctx,
		NATSPausedBucket,
		NATSPausedKey(util.AudienceStr(audience), pipelineID),
	); err != nil {
		if err == nats.ErrKeyNotFound {
			return false, nil
		}

		return false, errors.Wrap(err, "error fetching pipeline pause state")
	}

	return true, nil
}

// DONE
func (s *Store) ResumePipeline(ctx context.Context, req *protos.ResumePipelineRequest) error {
	llog := s.log.WithField("method", "ResumePipeline")
	llog.Debug("received request to resume pipeline")

	paused, err := s.IsPaused(ctx, req.Audience, req.PipelineId)
	if err != nil {
		return errors.Wrap(err, "error checking if pipeline is paused")
	}

	if !paused {
		llog.Debugf("pipeline '%s' not paused; nothing to do", req.PipelineId)
		return nil
	}

	llog.Debugf("pipeline '%s' paused; removing pause state now", req.PipelineId)
	if err := s.options.NATSBackend.Delete(
		ctx,
		NATSPausedBucket,
		NATSPausedKey(util.AudienceStr(req.Audience), req.PipelineId),
	); err != nil {
		return errors.Wrap(err, "error deleting pipeline pause state")
	}

	return nil
}

// DONE
func (s *Store) AddAudience(ctx context.Context, req *protos.NewAudienceRequest) error {
	llog := s.log.WithField("method", "AddAudience")
	llog.Debug("received request to add audience")

	if err := s.options.NATSBackend.Put(
		ctx,
		NATSLiveBucket,
		NATSLiveKey(req.SessionId, s.options.NodeName, util.AudienceStr(req.Audience)),
		nil,
		NATSLiveTTL,
	); err != nil {
		return errors.Wrap(err, "error saving audience to NATS")
	}

	return nil
}

func (o *Options) validate() error {
	if o == nil {
		return errors.New("options cannot be nil")
	}

	if o.NodeName == "" {
		return errors.New("node name cannot be empty")
	}

	if o.NATSBackend == nil {
		return errors.New("NATS backend cannot be nil")
	}

	if o.ShutdownCtx == nil {
		return errors.New("shutdown context cannot be nil")
	}

	return nil
}
