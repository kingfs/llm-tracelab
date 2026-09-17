import contextlib
import hashlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
from validate_atif import validate_record, validate_file, generate_schema


class ATIFValidationTests(unittest.TestCase):
    def example(self):
        return {"schema_version": "ATIF-v1.8", "agent": {"name": "codex", "version": "unknown"}, "steps": [{"step_id": 1, "source": "agent", "message": "done", "tool_calls": [{"tool_call_id": "c1", "function_name": "shell", "arguments": {}}], "observation": {"results": [{"source_call_id": "c1", "content": "ok"}]}}]}

    def test_valid(self):
        validate_record(self.example())

    def test_semantic_failures(self):
        cases = []
        for field, value in [("step_id", 2), ("source", "user"), ("timestamp", "not-a-date")]:
            data = self.example()
            data["steps"][0][field] = value
            cases.append(data)
        data = self.example()
        data["steps"][0]["observation"]["results"][0]["source_call_id"] = "other-step"
        cases.append(data)
        data = self.example()
        data["steps"][0].update(llm_call_count=0, metrics={"prompt_tokens": 3})
        cases.append(data)
        data = self.example()
        data["schema_version"] = "ATIF-v1.7"
        cases.append(data)
        data = self.example()
        data["steps"][0]["message"] = [{"type": "audio", "source": {"media_type": "image/png", "path": "x.png"}}]
        cases.append(data)
        for data in cases:
            with self.subTest(data=data), self.assertRaises(ValueError):
                validate_record(data)

    def test_audio_v18(self):
        data = self.example()
        data["steps"][0]["message"] = [{"type": "audio", "source": {"media_type": "audio/wav", "path": "clip.wav", "duration_sec": 1.5}}]
        validate_record(data)

    def test_multi_record_file_and_empty(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "test.jsonl"
            with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                path.write_text((json.dumps(self.example()) + "\n") * 2)
                self.assertTrue(validate_file(path))
                path.write_text("")
                self.assertFalse(validate_file(path))
                path.write_text(json.dumps(self.example()) + "\nnot-json\n")
                self.assertFalse(validate_file(path))

    def test_schema_and_upstream_sources_are_reproducible(self):
        root = SCRIPTS.parent
        schema = json.loads((root / "internal/trajectory/testdata/atif.schema.json").read_text())
        self.assertEqual(schema, generate_schema())
        manifest = json.loads((SCRIPTS / "atif_vendor/upstream.json").read_text())
        for name, expected in manifest["sha256"].items():
            self.assertEqual(hashlib.sha256((SCRIPTS / "atif_vendor/harbor/models/trajectories" / name).read_bytes()).hexdigest(), expected)


if __name__ == "__main__":
    unittest.main()
