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

// WrapStreamingClient must return a real handler: connect panics on a nil one.
func (l loggingInterceptor) WrapStreamingClient(handler connect.StreamingClientFunc) connect.StreamingClientFunc {
	return handler
}

// WrapStreamingHandler must return a real handler: connect panics on a nil one.
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
		status := connect.CodeUnknown.String()
		if connectErr := new(connect.Error); errors.As(responseErr, &connectErr) {
			status = connectErr.Code().String()
		}
		l.logger.ErrorContext(
			ctx, "request finished",
			append(args,
				"status", status,
				"error", responseErr,
			)...,
		)
		return response, responseErr
	}
}
