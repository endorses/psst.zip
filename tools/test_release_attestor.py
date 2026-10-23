"""Tag events must identify the same public source used by normal action signing."""

import copy
import unittest

from github_release_attestor import validate_tag_push_event
from release_artifacts import InvalidRelease


class TagPushIdentity(unittest.TestCase):
    def test_lightweight_and_annotated_tags_and_invalid_events(self):
        args = {
            "repository": "endorses/psst.zip",
            "version": "v1.2.3",
            "commit": "a" * 40,
        }
        event = {
            "repository": {"visibility": "public", "full_name": args["repository"]},
            "ref": "refs/tags/v1.2.3",
            "after": "a" * 40,
            "head_commit": {"id": "a" * 40},
        }
        for after in ("a" * 40, "b" * 40):
            validate_tag_push_event(event | {"after": after}, **args)
        for change in (
            {"head_commit": None},
            {"head_commit": {"id": "b" * 40}},
            {"after": "short"},
            {"ref": "refs/heads/main"},
            {"repository": {"visibility": "private", "full_name": args["repository"]}},
        ):
            with self.subTest(change=change), self.assertRaises(InvalidRelease):
                validate_tag_push_event(copy.deepcopy(event) | change, **args)


if __name__ == "__main__":
    unittest.main()
