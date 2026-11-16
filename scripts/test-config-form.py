#!/usr/bin/env python3
"""Tests for scripts/config_form.py: the configuration a hub accepts (#494), with the hub's own cases
(nwverse/persona server/internal/extension/schema_test.go). Run: python3 -I scripts/test-config-form.py."""
import importlib.util
import json
import pathlib
import unittest

spec = importlib.util.spec_from_file_location('config_form', pathlib.Path(__file__).with_name('config_form.py'))
config_form = importlib.util.module_from_spec(spec)
spec.loader.exec_module(config_form)

FORM = json.loads('''{"type": "object", "properties": {
  "url": {"type": "string", "title": "地址", "format": "uri"},
  "mode": {"type": "string", "oneOf": [{"const": "fast", "title": "快"}, {"const": "safe", "title": "稳"}], "default": "safe"},
  "count": {"type": "integer", "minimum": 1, "maximum": 10, "default": 3},
  "ratio": {"type": "number", "minimum": 0, "maximum": 1},
  "on": {"type": "boolean"},
  "mail": {"type": "string", "format": "email"},
  "host": {"type": "string", "format": "hostname"},
  "code": {"type": "string", "pattern": "^[A-Z]{3}$", "minLength": 3, "maxLength": 3},
  "tags": {"type": "array", "items": {"type": "string", "enum": ["a", "b", "c"]}, "uniqueItems": true, "maxItems": 2},
  "proxy": {"type": "object", "properties": {"host": {"type": "string"}, "port": {"type": "integer", "default": 8080}}, "required": ["host"]},
  "machines": {"type": "array", "items": {"type": "string", "format": "machine", "x-os": ["darwin"]}, "x-confirm": "Sure?"}
}, "required": ["url"]}''')


def refused(schema, manifest=None):
    m = dict(manifest or {})
    m["configuration"] = schema
    try:
        config_form.check(m)
    except config_form.Refused as e:
        return str(e)
    return ""


class SubsetTests(unittest.TestCase):
    def test_the_hubs_form_is_accepted(self):
        self.assertEqual(refused(FORM), "")
        self.assertEqual(refused(None), "")
        self.assertEqual(refused({}), "")

    def test_what_the_hub_refuses_is_refused(self):
        # The hub's TestSchemaRefusesOutsideTheSubset, minus the duplicate-key case (manifest.load refuses that).
        for raw in (
            '{"type": "array"}',
            '{"type": "object", "properties": {}, "additionalProperties": true}',
            '{"type": "object", "properties": {"a": {"type": "string", "$ref": "#/x"}}}',
            '{"type": "object", "properties": {"a": {"anyOf": []}}}',
            '{"type": "object", "properties": {"a": {"type": "null"}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "minimum": 1}}}',
            '{"type": "object", "properties": {"a": {"type": "array", "items": {"type": "object", "properties": {}}}}}',
            '{"type": "object", "properties": {"a": {"type": "object", "properties": {"b": {"type": "object", "properties": {}}}}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "format": "date"}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "enum": [1]}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "default": 1}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "oneOf": [{"const": "x", "if": {}}]}}}',
            '{"type": "object", "properties": {"a": {"type": "string"}}, "required": ["b"]}',
            '{"type": "object", "properties": {"a b": {"type": "string"}}}',
            '{"type": "object", "properties": {"on": {"type": "boolean", "x-confirm": true}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "x-os": ["darwin"]}}}',
            '{"type": "object", "properties": {"a": {"type": "string", "format": "machine", "x-os": ["beos"]}}}',
            '{"type": "object", "properties": {"n": {"type": "integer", "default": 1.5}}}',
            '{"type": "object", "properties": {"n": {"type": "integer", "minimum": 2, "default": 1}}}',
            '{"type": "object", "properties": {"t": {"type": "array", "items": {"type": "string"}, "maxItems": 1, "default": ["a", "b"]}}}',
        ):
            self.assertTrue(refused(json.loads(raw)), raw)

    def test_a_list_of_objects_says_where(self):
        # weather up to 0.2.0 (#493): what the hub writes in its catalog warning.
        weather = {"type": "object", "properties": {"cities": {"type": "array", "items": {"type": "object", "properties": {
            "name": {"type": "string"}}}, "default": [{"name": "上海"}]}}}
        self.assertIn("cities.items", refused(weather))
        self.assertIn("an object only at the first level", refused(weather))
        fixed = {"type": "object", "properties": {"cities": {"type": "array", "items": {"type": "string", "minLength": 1},
                                                            "maxItems": 8, "default": ["上海"]}}}
        self.assertEqual(refused(fixed), "")

    def test_an_invalid_pattern_only_go_reads_is_left_to_the_hub(self):
        self.assertEqual(refused({"type": "object", "properties": {"a": {"type": "string", "pattern": r"^\p{Han}+$"}}}), "")


class ConfiguredTests(unittest.TestCase):
    def schema(self, **field):
        return {"type": "object", "properties": {"url": dict({"type": "string", "format": "uri"}, **field)}}

    def test_a_config_network_entry_needs_a_uri_field_without_default(self):
        self.assertEqual(refused(self.schema(), {"permissions": {"network": ["config:url", "example.com"]}}), "")
        self.assertEqual(refused(self.schema(), {"permissions": {"network": ["config:url:http"]}}), "")
        self.assertIn("has a default", refused(self.schema(default="https://example.com"), {"permissions": {"network": ["config:url"]}}))
        self.assertIn("no field", refused(self.schema(), {"permissions": {"network": ["config:other"]}}))
        self.assertIn("format uri", refused({"type": "object", "properties": {"url": {"type": "string"}}},
                                             {"permissions": {"network": ["config:url"]}}))
        self.assertIn("config:<field>", refused(self.schema(), {"permissions": {"network": ["config:"]}}))

    def test_a_secret_hint_is_one_the_hub_knows(self):
        self.assertEqual(refused(None, {"secrets": [{"name": "key", "hint": "last4"}]}), "")
        self.assertIn("hint", refused(None, {"secrets": [{"name": "key", "hint": "first4"}]}))


if __name__ == '__main__':
    unittest.main()
