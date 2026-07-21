package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
)

type loggingInterceptor struct {
	logger *slog.Logger
}

// WrapStreamingClient implements [connect.Interceptor]. grpcview issues no
// outbound streaming calls of its own, so this is a pass-through; returning nil
// (the previous stub) would break any future streaming client.
func (l loggingInterceptor) WrapStreamingClient(handler connect.StreamingClientFunc) connect.StreamingClientFunc {
	return handler
}

// WrapStreamingHandler implements [connect.Interceptor]. It logs each streaming
// RPC's completion (latency + status), mirroring WrapUnary. Returning nil here
// (the previous unary-only stub) made connect call a nil implementation and
// panic on the first streaming RPC (InvokeStreaming).
func (l loggingInterceptor) WrapStreamingHandler(handler connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		start := time.Now()
		err := handler(ctx, conn)

		args := []any{
			"protocol", conn.Peer().Protocol,
			"address", conn.Peer().Addr,
			"procedure", conn.Spec().Procedure,
			"latency", time.Since(start),
		}

		if err == nil {
			l.logger.InfoContext(ctx, "stream finished", append(args, "status", "ok")...)
			return nil
		}

		status := connect.CodeUnknown.String()
		if connectErr := new(connect.Error); errors.As(err, &connectErr) {
			status = connectErr.Code().String()
		}
		l.logger.ErrorContext(ctx, "stream finished", append(args, "status", status, "error", err)...)
		return err
	}
}

// WrapUnary implements [connect.Interceptor].
func (l loggingInterceptor) WrapUnary(handler connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		response, responseErr := handler(ctx, req)
		end := time.Now()

		latency := end.Sub(start)
		args := []any{
			"protocol", req.Peer().Protocol,
			"address", req.Peer().Addr,
			"procedure", req.Spec().Procedure,
			"latency", latency,
		}

		if responseErr == nil {
			l.logger.InfoContext(
				ctx, "request finished",
				append(args,
					"status", "ok",
				)...,
			)
			return response, nil
		}
		if connectErr := new(connect.Error); errors.As(responseErr, &connectErr) {
			args = append(args,
				"status", connectErr.Code(),
			)
		}

		l.logger.ErrorContext(
			ctx, "request finished",
			append(args,
				"status", connect.CodeUnknown,
				"error", responseErr,
			)...,
		)
		return response, responseErr
	}
}
