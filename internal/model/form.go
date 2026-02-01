// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package model contains domain models and constants for the application.
package model

// Form field type constants
const (
	FieldTypeText     = "text"
	FieldTypeEmail    = "email"
	FieldTypeTextarea = "textarea"
	FieldTypeNumber   = "number"
	FieldTypeSelect   = "select"
	FieldTypeRadio    = "radio"
	FieldTypeCheckbox = "checkbox"
	FieldTypeDate     = "date"
	FieldTypeFile     = "file"
	FieldTypeCaptcha  = "captcha"
)

// ValidFieldTypes returns all valid form field types.
func ValidFieldTypes() []string {
	return []string{
		FieldTypeText,
		FieldTypeEmail,
		FieldTypeTextarea,
		FieldTypeNumber,
		FieldTypeSelect,
		FieldTypeRadio,
		FieldTypeCheckbox,
		FieldTypeDate,
		FieldTypeFile,
		FieldTypeCaptcha,
	}
}

// IsValidFieldType checks if a field type is valid.
func IsValidFieldType(fieldType string) bool {
	for _, t := range ValidFieldTypes() {
		if t == fieldType {
			return true
		}
	}
	return false
}
