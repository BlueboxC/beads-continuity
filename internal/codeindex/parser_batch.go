package codeindex

import (
	"encoding/json"
	"errors"
)

const parserOutputBudgetRevision = "parser-output-budget-v1\n"

func parseBoundedBatch(inputs []parserInput, parseBatch func([]parserInput) (parserOutput, error)) (parserOutput, error) {
	result, err := parseBatch(inputs)
	if err != nil {
		if !errors.Is(err, errParserStdoutLimit) || len(inputs) == 0 {
			return parserOutput{}, err
		}
		if len(inputs) == 1 {
			return parserOutput{Files: []File{{Path: inputs[0].Path, ParseError: "ASTLimit"}}}, nil
		}
		middle := len(inputs) / 2
		left, err := parseBoundedBatch(inputs[:middle], parseBatch)
		if err != nil {
			return parserOutput{}, err
		}
		right, err := parseBoundedBatch(inputs[middle:], parseBatch)
		if err != nil {
			return parserOutput{}, err
		}
		if left.Python == "" {
			left.Python = right.Python
		}
		if left.Node == "" {
			left.Node = right.Node
		}
		if left.TypeScript == "" {
			left.TypeScript = right.TypeScript
		}
		left.Files = append(left.Files, right.Files...)
		return left, nil
	}
	for i, file := range result.Files {
		encoded, err := json.Marshal(file)
		if err != nil {
			return parserOutput{}, err
		}
		if len(encoded) > maxJSONBytes-64*1024 {
			result.Files[i] = File{Path: file.Path, ParseError: "ASTLimit"}
		}
	}
	return result, nil
}
