package state

import "sync"

type Engine int

const (
	Idle Engine = iota
	Starting
	StreamActive
	Scanning
	SelfHealing
	ManualIntervention
)

func (s Engine) Name() string {
	switch s {
	case Starting:
		return "STARTING"
	case StreamActive:
		return "STREAM_ACTIVE"
	case Scanning:
		return "SCANNING"
	case SelfHealing:
		return "SELF_HEALING"
	case ManualIntervention:
		return "MANUAL_INTERVENTION"
	default:
		return "IDLE"
	}
}

func (s Engine) Title() string {
	switch s {
	case Starting:
		return "Подключение"
	case StreamActive:
		return "Трансляция"
	case Scanning:
		return "В лекции"
	case SelfHealing:
		return "Восстановление"
	case ManualIntervention:
		return "Нужно вмешательство"
	default:
		return "Ожидание"
	}
}

var transitions = map[Engine][]Engine{
	Idle:               {Starting},
	Starting:           {StreamActive, Idle, SelfHealing},
	StreamActive:       {Scanning, SelfHealing, Idle},
	Scanning:           {SelfHealing, ManualIntervention, StreamActive, Idle},
	SelfHealing:        {Scanning, StreamActive, ManualIntervention, Idle},
	ManualIntervention: {Scanning, StreamActive, Idle},
}

type Machine struct {
	mu       sync.Mutex
	state    Engine
	onChange func(prev, cur Engine)
}

func New(onChange func(prev, cur Engine)) *Machine { return &Machine{onChange: onChange} }

func (m *Machine) State() Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Machine) Active() bool { return m.State() != Idle }

func (m *Machine) Transition(target Engine) bool {
	m.mu.Lock()
	cur := m.state
	if cur == target {
		m.mu.Unlock()
		return true
	}
	allowed := false
	for _, t := range transitions[cur] {
		if t == target {
			allowed = true
			break
		}
	}
	if !allowed {
		m.mu.Unlock()
		return false
	}
	m.state = target
	fn := m.onChange
	m.mu.Unlock()
	if fn != nil {
		fn(cur, target)
	}
	return true
}

func (m *Machine) Reset() {
	m.mu.Lock()
	prev := m.state
	m.state = Idle
	fn := m.onChange
	m.mu.Unlock()
	if prev != Idle && fn != nil {
		fn(prev, Idle)
	}
}
