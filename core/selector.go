package core

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
)

type Selector struct {
	cores     map[string]Core
	ordered   []Core
	nodes     sync.Map
	lifecycle sync.Mutex
	started   bool
	closed    bool
	closeErr  error
}

func NewSelector(c []conf.CoreConfig) (result Core, err error) {
	s := &Selector{cores: make(map[string]Core, len(c)), ordered: make([]Core, 0, len(c))}
	defer func() {
		if p := recover(); p != nil {
			result = nil
			err = fmt.Errorf("construct core: %v", p)
		}
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	for _, t := range c {
		name := t.Name
		if name == "" {
			name = t.Type
		}
		if _, exists := s.cores[name]; exists {
			return nil, fmt.Errorf("duplicate core name %q", name)
		}
		f, ok := cores[strings.ToLower(t.Type)]
		if !ok {
			return nil, errors.New("unknown core type: " + t.Type)
		}
		core1, err := f(&t)
		if core1 != nil {
			// Even a constructor returning a partial result with an error has
			// transferred resources that need to be rolled back.
			s.ordered = append(s.ordered, core1)
		}
		if err != nil {
			return nil, err
		}
		if core1 == nil {
			return nil, fmt.Errorf("core %q constructor returned nil", name)
		}
		s.cores[name] = core1
	}
	return s, nil
}

func (s *Selector) Start() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.closed {
		return errors.New("core selector is closed")
	}
	if s.started {
		return nil
	}
	for _, c := range s.ordered {
		if err := c.Start(); err != nil {
			return errors.Join(err, s.closeLocked())
		}
	}
	s.started = true
	return nil
}

func (s *Selector) Close() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	return s.closeLocked()
}

func (s *Selector) closeLocked() error {
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	// Include unstarted cores: constructors may already own loggers, sockets,
	// timers, and other resources. Reverse order mirrors construction.
	for i := len(s.ordered) - 1; i >= 0; i-- {
		s.closeErr = errors.Join(s.closeErr, s.ordered[i].Close())
	}
	return s.closeErr
}

func isSupported(protocol string, protocols []string) bool {
	for i := range protocols {
		if protocol == protocols[i] {
			return true
		}
	}
	return false
}

func (s *Selector) AddNode(tag string, info *panel.NodeInfo, option *conf.Options) error {
	var core Core
	if len(option.CoreName) > 0 {
		// use name to select core
		if c, ok := s.cores[option.CoreName]; ok {
			core = c
		}
	} else {
		// use type to select core
		for _, c := range s.cores {
			if len(option.Core) == 0 {
				if !isSupported(info.Type, c.Protocols()) {
					continue
				}
			} else if option.Core != c.Type() {
				continue
			}
			core = c
		}
	}
	if core == nil {
		return errors.New("the node type is not support")
	}
	if len(option.Core) == 0 {
		option.Core = core.Type()
		err := option.UnmarshalJSON(option.RawOptions)
		if err != nil {
			return fmt.Errorf("unmarshal option error: %s", err)
		}
		option.RawOptions = nil
	}
	err := core.AddNode(tag, info, option)
	if err != nil {
		return err
	}
	s.nodes.Store(tag, core)
	return nil
}

func (s *Selector) DelNode(tag string) error {
	if t, e := s.nodes.Load(tag); e {
		err := t.(Core).DelNode(tag)
		if err != nil {
			return err
		}
		s.nodes.Delete(tag)
		return nil
	}
	return errors.New("the node is not have")
}

func (s *Selector) AddUsers(p *AddUsersParams) (added int, err error) {
	t, e := s.nodes.Load(p.Tag)
	if !e {
		return 0, errors.New("the node is not have")
	}
	return t.(Core).AddUsers(p)
}

func (s *Selector) UpdateUsers(p *UpdateUsersParams) error {
	t, ok := s.nodes.Load(p.Tag)
	if !ok {
		return errors.New("the node is not have")
	}
	return ApplyUserUpdate(t.(Core), p)
}

func (s *Selector) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	t, e := s.nodes.Load(tag)
	if !e {
		return nil, errors.New("the node is not have")
	}
	return t.(Core).GetUserTrafficSlice(tag, reset)
}

func (s *Selector) ReturnUserTraffic(tag string, traffic []panel.UserTraffic) error {
	t, e := s.nodes.Load(tag)
	if !e {
		return errors.New("the node is not have")
	}
	return t.(Core).ReturnUserTraffic(tag, traffic)
}

func (s *Selector) DelUsers(users []panel.UserInfo, tag string, info *panel.NodeInfo) error {
	t, e := s.nodes.Load(tag)
	if !e {
		return errors.New("the node is not have")
	}
	return t.(Core).DelUsers(users, tag, info)
}

func (s *Selector) UpdateNodeReportMinTraffic(tag string, info *panel.NodeInfo, config *conf.Options) {
	t, e := s.nodes.Load(tag)
	if !e {
		return
	}
	t.(Core).UpdateNodeReportMinTraffic(tag, info, config)
}

func (s *Selector) UpdateDNS(tag string, info *panel.NodeInfo) error {
	t, e := s.nodes.Load(tag)
	if !e {
		return errors.New("the node is not have")
	}
	return t.(Core).UpdateDNS(tag, info)
}

func (s *Selector) AddNodeCustomOutbounds(info *panel.NodeInfo, opts *conf.Options) error {
	var errs []error
	for _, core := range s.cores {
		if err := core.AddNodeCustomOutbounds(info, opts); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (s *Selector) Protocols() []string {
	protocols := make([]string, 0)
	for i := range s.cores {
		protocols = append(protocols, s.cores[i].Protocols()...)
	}
	return protocols
}

func (s *Selector) Type() string {
	t := "Selector("
	var flag bool
	for n, c := range s.cores {
		if flag {
			t += " "
		} else {
			flag = true
		}
		if len(n) == 0 {
			t += c.Type()
		} else {
			t += n
		}
	}
	t += ")"
	return t
}
