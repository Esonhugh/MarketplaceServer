package git

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
)

func (s *Service) AdvertiseDistribution(ctx context.Context, projection gitservice.ImmutableProjection, stdout, stderr io.Writer) error {
	path, err := s.existingProjectionPath(projection)
	if err != nil {
		return err
	}
	if s.advertiseTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.advertiseTimeout)
		defer cancel()
	}
	var advertisement bytes.Buffer
	const service = "git-upload-pack"
	if _, err := fmt.Fprintf(&advertisement, "%04x# service=%s\n", len("# service="+service+"\n")+4, service); err != nil {
		return err
	}
	if _, err := io.WriteString(&advertisement, "0000"); err != nil {
		return err
	}
	if err := s.runGit(ctx, nil, &advertisement, stderr, "upload-pack", "--stateless-rpc", "--advertise-refs", path); err != nil {
		return err
	}
	_, err = advertisement.WriteTo(stdout)
	return err
}

func (s *Service) UploadDistribution(ctx context.Context, projection gitservice.ImmutableProjection, stdin io.Reader, stdout, stderr io.Writer) error {
	path, err := s.existingProjectionPath(projection)
	if err != nil {
		return err
	}
	if s.serviceTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.serviceTimeout)
		defer cancel()
	}
	return s.runGit(ctx, stdin, stdout, stderr, "upload-pack", "--stateless-rpc", path)
}
