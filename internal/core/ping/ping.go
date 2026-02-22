package ping

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Target struct {
	IP       string
	Location string
}

type Result struct {
	Target      Target
	Successful  int
	Total       int
	MinTime     time.Duration
	AvgTime     time.Duration
	MaxTime     time.Duration
	SuccessRate float64
	IsReachable bool
	Status      string
}

type EchoResult interface {
	GetRTT() time.Duration
	IsReached() bool
}

type EchoFunc func(host string, ttl int, timeout time.Duration) (EchoResult, error)

type Engine struct {
	config Config
}

type Config struct {
	Count               int
	Timeout             time.Duration
	Delay               time.Duration
	MaxConcurrent       int
	ConsecutiveTimeouts int
}

func NewEngine(cfg Config) *Engine {
	return &Engine{config: cfg}
}

func (e *Engine) Ping(ctx context.Context, targets []Target, echoFn EchoFunc) []Result {
	if echoFn == nil {
		return nil
	}

	results := make([]Result, len(targets))
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, e.config.MaxConcurrent)

	for i, target := range targets {
		wg.Add(1)
		go func(idx int, t Target) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[idx] = Result{
					Target:      t,
					Status:      "cancelled",
					IsReachable: false,
				}
				return
			}

			results[idx] = e.pingTarget(ctx, t, echoFn)
		}(i, target)
	}

	wg.Wait()
	return results
}

func (e *Engine) pingTarget(ctx context.Context, target Target, echoFn EchoFunc) Result {
	result := Result{
		Target: target,
		Total:  e.config.Count,
	}

	var successCount int
	var totalRTT time.Duration
	var minRTT, maxRTT time.Duration
	consecutiveTimeouts := 0

	for i := 0; i < e.config.Count; i++ {
		select {
		case <-ctx.Done():
			result.Status = "cancelled"
			e.calculateStats(&result, successCount, totalRTT, minRTT, maxRTT)
			return result
		default:
		}

		echoResult, err := echoFn(target.IP, 64, e.config.Timeout)
		if err != nil {
			consecutiveTimeouts++
			if consecutiveTimeouts >= e.config.ConsecutiveTimeouts {
				result.Status = fmt.Sprintf("timeout after %d attempts", e.config.ConsecutiveTimeouts)
				e.calculateStats(&result, successCount, totalRTT, minRTT, maxRTT)
				return result
			}
			continue
		}

		if echoResult.IsReached() {
			rtt := echoResult.GetRTT()
			successCount++
			totalRTT += rtt

			if minRTT == 0 || rtt < minRTT {
				minRTT = rtt
			}
			if rtt > maxRTT {
				maxRTT = rtt
			}
			consecutiveTimeouts = 0
		} else {
			consecutiveTimeouts++
			if consecutiveTimeouts >= e.config.ConsecutiveTimeouts {
				result.Status = fmt.Sprintf("timeout after %d attempts", e.config.ConsecutiveTimeouts)
				e.calculateStats(&result, successCount, totalRTT, minRTT, maxRTT)
				return result
			}
		}

		if i < e.config.Count-1 && e.config.Delay > 0 {
			time.Sleep(e.config.Delay)
		}
	}

	e.calculateStats(&result, successCount, totalRTT, minRTT, maxRTT)
	if result.IsReachable {
		result.Status = "ok"
	} else {
		result.Status = "unreachable"
	}

	return result
}

func (e *Engine) calculateStats(result *Result, successCount int, totalRTT, minRTT, maxRTT time.Duration) {
	result.Successful = successCount
	result.SuccessRate = float64(successCount) / float64(result.Total) * 100
	result.IsReachable = successCount > 0
	result.MinTime = minRTT
	result.MaxTime = maxRTT

	if successCount > 0 {
		result.AvgTime = totalRTT / time.Duration(successCount)
	}
}
