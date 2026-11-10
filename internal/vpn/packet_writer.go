package vpn

import (
	"bytes"
	"context"
	"errors"

	oc "github.com/sagernet/sing-openconnect"
)

type packetWrite struct {
	ctx    context.Context
	packet []byte
	result chan error
}

func (s *packetSession) sendPacket(ctx context.Context, packet []byte) error {
	request := packetWrite{ctx: ctx, packet: bytes.Clone(packet), result: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.life.Done():
		return s.life.Err()
	case s.writes <- request:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.life.Done():
		return s.life.Err()
	case err := <-request.result:
		return err
	}
}

func (s *packetSession) writeLoop() {
	for {
		select {
		case <-s.life.Done():
			return
		case request := <-s.writes:
			if err := request.ctx.Err(); err != nil {
				request.result <- err
				continue
			}
			if err := s.life.Err(); err != nil {
				return
			}
			err := s.writePacket(request.packet)
			request.result <- err
			if err != nil && !errors.Is(err, oc.ErrDataChannelNotReady) {
				s.invalidate(err)
				return
			}
		}
	}
}
