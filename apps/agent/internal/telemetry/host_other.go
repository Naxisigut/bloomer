//go:build !darwin

package telemetry

import "context"

// This first implementation targets the shipped macOS agent. Other hosts can
// still run the service; unsupported metrics are explicit, never synthetic zero.
func (p *Sampler) hardware(context.Context, *Hardware)                {}
func (p *Sampler) host(context.Context, *Sample)                      {}
func processAlive(int, *int64) (bool, error)                          { return false, errUnsupported }
func (p *Sampler) processCPU(context.Context, int) (*float64, string) { return nil, Unsupported }
