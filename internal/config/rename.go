package config

import "time"

const (
	replaceRetryBudget = 2 * time.Second
	replaceRetryFirst  = 2 * time.Millisecond
	replaceRetryMax    = 128 * time.Millisecond
)

func replaceWithRetry(rename func(string, string) error, transient func(error) bool, sleep func(time.Duration), src, dst string) error {
	delay := replaceRetryFirst
	var waited time.Duration
	for {
		err := rename(src, dst)
		if err == nil || !transient(err) || waited >= replaceRetryBudget {
			return err
		}
		sleep(delay)
		waited += delay
		if delay < replaceRetryMax {
			delay *= 2
		}
	}
}
