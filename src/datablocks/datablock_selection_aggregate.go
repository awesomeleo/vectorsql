// Copyright 2020 The VectorSQL Authors.
//
// Code is licensed under Apache License, Version 2.0.

package datablocks

import (
	"sync"

	"columns"
	"datatypes"
	"datavalues"
	"expressions"
	"planners"
)

func (block *DataBlock) AggregateSelectionByPlan(plan *planners.MapPlan) (*DataBlock, error) {
	projects := plan

	projectExprs, err := planners.BuildExpressions(projects)
	if err != nil {
		return nil, err
	}

	// Get all base fields.
	fields, err := expressions.VariableValues(projectExprs...)
	if err != nil {
		return nil, err
	}

	columnmap := make(map[string]struct{})
	for i := range block.values {
		columnmap[block.values[i].column.Name] = struct{}{}
	}

	rows := block.NumRows()
	if rows == 0 {
		// If empty, returns header only.
		cols := make([]*columns.Column, len(projectExprs))
		for i, expr := range projectExprs {
			cols[i] = columns.NewColumn(expr.String(), datatypes.NewStringDataType())
		}
		return NewDataBlock(cols), nil
	} else {
		var errs []error
		var wg sync.WaitGroup

		// Update.
		for _, expr := range projectExprs {
			name := expr.String()
			if _, ok := columnmap[name]; ok {
				continue
			}

			wg.Add(1)
			go func(expr expressions.IExpression) {
				defer wg.Done()

				// Compute the column.
				it, err := block.MixsIterator(fields)
				if err != nil {
					errs = append(errs, err)
					return
				}

				params := make(expressions.Map)
				for it.Next() {
					mixed := it.Value()
					for j := range mixed {
						params[it.Column(j).Name] = mixed[j]
					}
					if _, err := expr.Update(params); err != nil {
						errs = append(errs, err)
						return
					}
				}
			}(expr)
		}
		wg.Wait()
		if len(errs) > 0 {
			return nil, errs[0]
		}

		// Final.
		lastidx := block.NumRows() - 1
		row := make([]*datavalues.Value, len(projectExprs))
		column := make([]*columns.Column, len(projectExprs))
		for i, expr := range projectExprs {
			name := expr.String()
			_, ok := columnmap[name]
			if ok {
				cv, err := block.DataBlockValue(expr.String())
				if err != nil {
					return nil, err
				}
				column[i] = cv.column
				row[i] = cv.values[block.seqs[lastidx]]
			} else {
				val, err := expr.Get()
				if err != nil {
					return nil, err
				}
				row[i] = val

				// Get the column type via the expression value.
				dtype, err := datatypes.GetDataTypeByValue(val)
				if err != nil {
					return nil, err
				}
				column[i] = columns.NewColumn(name, dtype)
			}
		}

		result := NewDataBlock(column)
		if err := result.WriteRow(row); err != nil {
			return nil, err
		}
		return result, nil
	}
}
