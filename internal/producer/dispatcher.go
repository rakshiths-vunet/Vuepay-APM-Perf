package producer

import (
	"fmt"
	"sync/atomic"

	"github.com/your-org/vuepay-producer/internal/journey"
)

type Dispatcher struct {
	sequence []journey.Journey
	index    atomic.Uint64
}

func NewDispatcher(journeys []journey.Journey, weights map[string]int) (*Dispatcher, error) {
	if len(journeys) == 0 {
		return nil, fmt.Errorf("no journeys configured")
	}

	jByName := make(map[string]journey.Journey, len(journeys))
	for _, j := range journeys {
		jByName[j.Name()] = j
	}

	sequence := make([]journey.Journey, 0)
	for _, j := range journeys {
		name := j.Name()
		weight, ok := weights[name]
		if !ok {
			continue
		}
		for i := 0; i < weight; i++ {
			sequence = append(sequence, j)
		}
	}

	for name := range weights {
		if _, ok := jByName[name]; !ok {
			return nil, fmt.Errorf("unknown journey in weight config: %s", name)
		}
	}

	if len(sequence) == 0 {
		return nil, fmt.Errorf("dispatcher sequence is empty")
	}

	return &Dispatcher{sequence: sequence}, nil
}

func (d *Dispatcher) Next() journey.Journey {
	i := d.index.Add(1)
	return d.sequence[(i-1)%uint64(len(d.sequence))]
}
