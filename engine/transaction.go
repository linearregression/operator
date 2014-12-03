package engine

import (
	"sync"

	"github.com/forklift/geppetto/event"
	"github.com/forklift/geppetto/unit"
)

func NewTransaction(e *Engine, u *unit.Unit) *Transaction {
	return &Transaction{engine: e, unit: u}
}

type Transaction struct {
	engine *Engine
	unit   *unit.Unit
	ch     chan<- *event.Event

	//Internals
	prepared bool
}

func (t *Transaction) Prepare() error {

	if t.prepared {
		return nil
	}

	/*if t.engine.HasUnit(t.unit.Name) {
		t.engine.Events <- event.NewEvent(t.unit.Name, event.StatusAlreadyLoaded)
		return errors.New("Unit is already loaded. Transaction canceled.")
	}*/

	deps, err := buildUnits(t.engine, t.unit.Service.Requires, t.unit.Service.Wants)
	if err != nil {
		return err
	}
	_ = deps

	err = t.unit.Prepare()
	if err != nil {
		t.unit.Service.Cleanup()
		return err
	}

	//NOTE: Is this a good idea? Can we attempt to reprepare a transaction if it fails?
	if err == nil {
		t.prepared = true
	}
	return err
}

func (t *Transaction) Start() error {

	for _, name := range t.unit.Service.Before {
		_ = name
		//t.engine.Start(t.unit)
	}
	return nil
}

func buildUnits(engine *Engine, unitlists ...[]string) (map[string]*Transaction, error) {

	errs := make(chan error)

	all := make(map[string]*Transaction)

	prepared := make([]chan *Transaction, len(unitlists))

	for _, units := range unitlists {
		prepared = append(prepared, prepareUnits(engine, errs, readUnits(engine, errs, units)))
	}

	for {
		select {
		case t := <-mergeTransactions(errs, prepared...):
			all[t.unit.Name] = t
		case err := <-errs:
			close(errs)
			for _, t := range all {
				t.unit.Service.Cleanup() //TODO: Log/Handle errors
			}
			return nil, err //TODO: should retrun the units so fa?
		}
	}
	//TODO: We shouldn't really ever reach here. Panic? Error?
	return all, nil
}

func readUnits(engine *Engine, errs chan error, names []string) chan *unit.Unit {
	units := make(chan *unit.Unit)

	go func() {
		defer close(units)

		for _, name := range names {

			//if engine.HasUnit(name) {
			//	continue
			//}

			u, err := unit.Read(name)
			if err != nil {
				errs <- err
			}

			select {
			case units <- u:
			case e := <-errs:
				errs <- e
				return
			}
		}
	}()

	return units
}

func prepareUnits(engine *Engine, errs chan error, units chan *unit.Unit) chan *Transaction {

	transactions := make(chan *Transaction)

	defer close(units)

	go func() {
		for unit := range units {

			transaction := NewTransaction(engine, unit)
			err := transaction.Prepare()
			if err != nil {
				errs <- err
				return
			}

			select {
			case transactions <- transaction:
			case e := <-errs:
				errs <- e
				return
			}

		}
	}()

	return transactions
}

func mergeTransactions(errs chan error, transactionChans ...chan *Transaction) chan *Transaction {

	transactions := make(chan *Transaction)

	var wg sync.WaitGroup
	wg.Add(len(transactionChans))

	for _, ch := range transactionChans {
		go func(uc chan *Transaction) {
			for {
				select {
				case t := <-ch:
					transactions <- t
				case e := <-errs:
					errs <- e
					break
				}
			}
			wg.Done()
		}(ch)
	}

	// Start a goroutine to close out once all the output goroutines are
	// done.  This must start after the wg.Add call.
	go func() {
		wg.Wait()
		close(errs)
	}()

	return transactions
}
