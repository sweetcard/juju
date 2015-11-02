// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrevisionworker_test

import (
	"time"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/worker"
	"github.com/juju/juju/worker/charmrevisionworker"
)

type WorkerSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&WorkerSuite{})

func (s *WorkerSuite) TestUpdatesImmediately(c *gc.C) {
	fix := newFixture(time.Minute)
	fix.cleanTest(c, func(_ worker.Worker) {
		fix.waitCall(c)
		fix.waitNoCall(c)
	})
	fix.facade.stub.CheckCallNames(c, "UpdateLatestRevisions")
}

func (s *WorkerSuite) TestNoMoreUpdatesUntilPeriod(c *gc.C) {
	fix := newFixture(time.Minute)
	fix.cleanTest(c, func(_ worker.Worker) {
		fix.waitCall(c)
		fix.clock.Advance(time.Minute - time.Nanosecond)
		fix.waitNoCall(c)
	})
	fix.facade.stub.CheckCallNames(c, "UpdateLatestRevisions")
}

func (s *WorkerSuite) TestUpdatesAfterPeriod(c *gc.C) {
	fix := newFixture(time.Minute)
	fix.cleanTest(c, func(_ worker.Worker) {
		fix.waitCall(c)
		fix.clock.Advance(time.Minute)
		fix.waitCall(c)
		fix.waitNoCall(c)
	})
	fix.facade.stub.CheckCallNames(c, "UpdateLatestRevisions", "UpdateLatestRevisions")
}

func (s *WorkerSuite) TestImmediateUpdateError(c *gc.C) {
	fix := newFixture(time.Minute)
	fix.facade.stub.SetErrors(
		errors.New("no updates for you"),
	)
	fix.dirtyTest(c, func(w worker.Worker) {
		fix.waitCall(c)
		c.Check(w.Wait(), gc.ErrorMatches, "no updates for you")
		fix.waitNoCall(c)
	})
	fix.facade.stub.CheckCallNames(c, "UpdateLatestRevisions")
}

func (s *WorkerSuite) TestDelayedUpdateError(c *gc.C) {
	fix := newFixture(time.Minute)
	fix.facade.stub.SetErrors(
		nil,
		errors.New("no more updates for you"),
	)
	fix.dirtyTest(c, func(w worker.Worker) {
		fix.waitCall(c)
		fix.clock.Advance(time.Minute)
		fix.waitCall(c)
		c.Check(w.Wait(), gc.ErrorMatches, "no more updates for you")
		fix.waitNoCall(c)
	})
	fix.facade.stub.CheckCallNames(c, "UpdateLatestRevisions", "UpdateLatestRevisions")
}

// workerFixture isolates a charmrevisionworker for testing.
type workerFixture struct {
	facade mockFacade
	clock  *coretesting.Clock
	period time.Duration
}

func newFixture(period time.Duration) workerFixture {
	return workerFixture{
		facade: newMockFacade(),
		clock:  coretesting.NewClock(time.Now()),
		period: period,
	}
}

type testFunc func(worker.Worker)

func (fix workerFixture) cleanTest(c *gc.C, test testFunc) {
	fix.runTest(c, test, true)
}

func (fix workerFixture) dirtyTest(c *gc.C, test testFunc) {
	fix.runTest(c, test, false)
}

func (fix workerFixture) runTest(c *gc.C, test testFunc, checkWaitErr bool) {
	w, err := charmrevisionworker.NewWorker(charmrevisionworker.WorkerConfig{
		Facade: fix.facade,
		Clock:  fix.clock,
		Period: fix.period,
	})
	c.Assert(err, jc.ErrorIsNil)
	defer func() {
		err := worker.Stop(w)
		if checkWaitErr {
			c.Check(err, jc.ErrorIsNil)
		}
	}()
	test(w)
}

func (fix workerFixture) waitCall(c *gc.C) {
	select {
	case <-fix.facade.calls:
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timed out")
	}
}

func (fix workerFixture) waitNoCall(c *gc.C) {
	select {
	case <-fix.facade.calls:
		c.Fatalf("unexpected facade call")
	case <-time.After(coretesting.ShortWait):
	}
}

// mockFacade records (and notifies of) calls made to UpdateLatestRevisions.
type mockFacade struct {
	stub  *testing.Stub
	calls chan struct{}
}

func newMockFacade() mockFacade {
	return mockFacade{
		stub:  &testing.Stub{},
		calls: make(chan struct{}, 1000),
	}
}

func (mock mockFacade) UpdateLatestRevisions() error {
	mock.stub.AddCall("UpdateLatestRevisions")
	mock.calls <- struct{}{}
	return mock.stub.NextErr()
}
