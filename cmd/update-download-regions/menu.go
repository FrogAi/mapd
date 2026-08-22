package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type jsonField struct {
	name  string
	value json.RawMessage
}

type jsonObject []jsonField

func parseJSONObject(data []byte) (jsonObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}

	var object jsonObject
	names := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name := token.(string)
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("duplicate JSON field %q", name)
		}
		names[name] = struct{}{}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		object = append(object, jsonField{name: name, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected data after JSON object")
		}
		return nil, err
	}
	return object, nil
}

func (object jsonObject) field(name string) (*jsonField, bool) {
	for index := range object {
		if object[index].name == name {
			return &object[index], true
		}
	}
	return nil, false
}

func (object *jsonObject) set(name string, value json.RawMessage) {
	if field, exists := object.field(name); exists {
		field.value = value
		return
	}
	*object = append(*object, jsonField{name: name, value: value})
}

func (object *jsonObject) delete(name string) {
	for index := range *object {
		if (*object)[index].name == name {
			*object = append((*object)[:index], (*object)[index+1:]...)
			return
		}
	}
}

func renderJSONObject(object jsonObject, indent int, newline string) json.RawMessage {
	if len(object) == 0 {
		return json.RawMessage("{}")
	}

	var output strings.Builder
	output.WriteByte('{')
	for index, field := range object {
		if index > 0 {
			output.WriteByte(',')
		}
		output.WriteString(newline)
		output.WriteString(strings.Repeat(" ", indent+2))
		name, _ := json.Marshal(field.name)
		output.Write(name)
		output.WriteString(": ")
		output.Write(bytes.TrimSpace(field.value))
	}
	output.WriteString(newline)
	output.WriteString(strings.Repeat(" ", indent))
	output.WriteByte('}')
	return json.RawMessage(output.String())
}

func inlineArchiveRanges(menu *jsonObject, ranges downloadRanges, newline string) error {
	for _, sectionName := range []string{"nation", "us_state"} {
		sectionField, exists := menu.field(sectionName)
		if !exists {
			continue
		}
		section, err := parseJSONObject(sectionField.value)
		if err != nil {
			return fmt.Errorf("%s: %w", sectionName, err)
		}

		for index := range section {
			location, err := parseJSONObject(section[index].value)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", sectionName, section[index].name, err)
			}
			if archiveRanges, exists := ranges[sectionName][section[index].name]; exists {
				encoded, _ := json.Marshal(archiveRanges)
				location.set("archive_ranges", bytes.ReplaceAll(encoded, []byte(","), []byte(", ")))
			} else {
				location.delete("archive_ranges")
			}
			section[index].value = renderJSONObject(location, 4, newline)
		}
		sectionField.value = renderJSONObject(section, 2, newline)
	}
	return nil
}
