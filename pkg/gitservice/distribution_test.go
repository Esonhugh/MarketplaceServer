package gitservice

import (
	"context"
	"io"
	"testing"
)

type distributionReaderContractStub struct{}

func (distributionReaderContractStub) AdvertiseDistribution(context.Context, ImmutableProjection, io.Writer, io.Writer) error {
	return nil
}

func (distributionReaderContractStub) UploadDistribution(context.Context, ImmutableProjection, io.Reader, io.Writer, io.Writer) error {
	return nil
}

func TestDistributionReaderContractIsReadOnly(t *testing.T) {
	var reader DistributionReader = distributionReaderContractStub{}
	if _, ok := any(reader).(interface {
		ReceivePack(context.Context, string, io.Reader, io.Writer, io.Writer) error
	}); ok {
		t.Fatal("DistributionReader exposes receive-pack")
	}
	if _, ok := any(reader).(interface {
		InitBareRepository(context.Context, string) error
	}); ok {
		t.Fatal("DistributionReader exposes repository creation")
	}
}
