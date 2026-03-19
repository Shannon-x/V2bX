package limiter

import (
	"fmt"
	"regexp"

	"github.com/InazumaV/V2bX/api/panel"
)

func (l *Limiter) CheckDomainRule(destination string) (reject bool) {
	// have rule
	for i := range l.DomainRules {
		if l.DomainRules[i].MatchString(destination) {
			reject = true
			break
		}
	}
	return
}

func (l *Limiter) CheckProtocolRule(protocol string) (reject bool) {
	for i := range l.ProtocolRules {
		if l.ProtocolRules[i] == protocol {
			reject = true
			break
		}
	}
	return
}

func (l *Limiter) UpdateRule(rule *panel.Rules) error {
	l.DomainRules = make([]*regexp.Regexp, 0, len(rule.Regexp))
	for i := range rule.Regexp {
		re, err := regexp.Compile(rule.Regexp[i])
		if err != nil {
			return fmt.Errorf("compile rule regexp %q error: %w", rule.Regexp[i], err)
		}
		l.DomainRules = append(l.DomainRules, re)
	}
	l.ProtocolRules = rule.Protocol
	return nil
}
