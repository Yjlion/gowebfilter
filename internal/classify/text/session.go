package text

import (
	"fmt"

	ort "github.com/yalue/onnxruntime_go"
)

// newSession loads modelPath (an optimum-exported DistilBERT sequence-
// classification ONNX graph) and allocates its I/O tensors, returning a
// *Model with its onnxruntime fields populated (tok/nsfwIdx are filled in
// by the caller, Load). Unlike internal/classify/image's detector, which
// assumes exactly one input, this binds tensors by name: an optimum
// export commonly has "input_ids" + "attention_mask" and sometimes an
// unused "token_type_ids", and that exact set isn't something to hardcode
// blindly across exporter versions.
func newSession(modelPath string, maxLen, numLabels int) (*Model, error) {
	inputInfo, outputInfo, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model %s: %w", modelPath, err)
	}
	if len(outputInfo) != 1 {
		return nil, fmt.Errorf("model %s must have exactly 1 output tensor, has %d", modelPath, len(outputInfo))
	}

	inputs := make(map[string]*ort.Tensor[int64], len(inputInfo))
	inputNames := make([]string, 0, len(inputInfo))
	inputValues := make([]ort.Value, 0, len(inputInfo))
	for _, info := range inputInfo {
		switch info.Name {
		case "input_ids", "attention_mask", "token_type_ids":
		default:
			destroyInputs(inputs)
			return nil, fmt.Errorf("model %s has unrecognized input %q (expected input_ids/attention_mask/token_type_ids)",
				modelPath, info.Name)
		}
		t, err := ort.NewEmptyTensor[int64](ort.NewShape(1, int64(maxLen)))
		if err != nil {
			destroyInputs(inputs)
			return nil, fmt.Errorf("allocate %s tensor: %w", info.Name, err)
		}
		inputs[info.Name] = t
		inputNames = append(inputNames, info.Name)
		inputValues = append(inputValues, t)
	}
	if _, ok := inputs["input_ids"]; !ok {
		destroyInputs(inputs)
		return nil, fmt.Errorf(`model %s has no "input_ids" input`, modelPath)
	}

	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(numLabels)))
	if err != nil {
		destroyInputs(inputs)
		return nil, fmt.Errorf("allocate output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		inputNames, []string{outputInfo[0].Name},
		inputValues, []ort.Value{outputTensor}, nil)
	if err != nil {
		destroyInputs(inputs)
		outputTensor.Destroy()
		return nil, fmt.Errorf("create session for %s: %w", modelPath, err)
	}

	return &Model{
		session: session,
		inputs:  inputs,
		logits:  outputTensor,
	}, nil
}

func destroyInputs(inputs map[string]*ort.Tensor[int64]) {
	for _, t := range inputs {
		t.Destroy()
	}
}
