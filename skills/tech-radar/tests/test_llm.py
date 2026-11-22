import unittest
from unittest import mock

from helpers import FakeLlm, art, arts
from tech_radar import DeepSeekLlm, LlmError, Summary, rank, summarize


class RankTest(unittest.TestCase):
    def test_rank_orders_by_score_and_caps(self):
        llm = FakeLlm([{"selected": [{"id": i, "score": i % 7} for i in range(14)]}])
        got = rank(llm, "interests", arts(14))
        self.assertEqual(len(got), 10)
        self.assertEqual(got[0].title, "t6")
        scores = [int(a.title[1:]) % 7 for a in got]
        self.assertEqual(scores, sorted(scores, reverse=True))

    def test_rank_tolerates_messy_ids(self):
        llm = FakeLlm([{"selected": [{"id": "1", "score": 9}, {"id": 1, "score": 8}, {"id": 99, "score": 10}, {"id": "x"}]}])
        self.assertEqual([a.title for a in rank(llm, "i", arts(3))], ["t1"])

    def test_rank_retries_once(self):
        llm = FakeLlm([LlmError("answer is not JSON"), {"selected": [{"id": 2, "score": 9}]}])
        self.assertEqual([a.title for a in rank(llm, "i", arts(3))], ["t2"])
        self.assertEqual(len(llm.calls), 2)

    def test_rank_gives_up_after_one_retry(self):
        llm = FakeLlm([LlmError("a"), {"selected": []}, {"selected": [{"id": 0, "score": 1}]}])
        with self.assertRaises(LlmError):
            rank(llm, "i", arts(3))
        self.assertEqual(len(llm.calls), 2)

    def test_rank_nothing_valid_raises(self):
        with self.assertRaises(LlmError):
            rank(FakeLlm([{"selected": []}, {"selected": []}]), "i", arts(3))
        with self.assertRaises(LlmError):
            rank(FakeLlm([{"other": 1}, {"selected": [{"id": 99}]}]), "i", arts(3))

    def test_rank_prompt_holds_interests_and_cut_descriptions(self):
        llm = FakeLlm([{"selected": [{"id": 0, "score": 5}]}])
        rank(llm, "Docker", [art("t0", description="x" * 1000)])
        self.assertIn("Docker", llm.calls[0][1])
        self.assertIn("x" * 400, llm.calls[0][1])
        self.assertNotIn("x" * 401, llm.calls[0][1])


class SummarizeTest(unittest.TestCase):
    def test_summarize_valid_answer(self):
        llm = FakeLlm([{"emoji": "🐳", "keywords": ["Docker", "Compose"], "summary": "S."}])
        self.assertEqual(summarize(llm, "i", art("t"), "b"), Summary("🐳", ["Docker", "Compose"], "S."))
        self.assertIn("b", llm.calls[0][1])

    def test_summarize_trims_keywords_to_five(self):
        llm = FakeLlm([{"emoji": "🐳", "keywords": ["a", "b", "c", "d", "e", "f"], "summary": "S."}])
        self.assertEqual(summarize(llm, "i", art("t"), "b").keywords, ["a", "b", "c", "d", "e"])

    def test_summarize_retries_once_then_falls_back(self):
        llm = FakeLlm([LlmError("bad"), {"emoji": "", "keywords": [], "summary": ""}])
        s = summarize(llm, "i", art("t", description="desc"), "body")
        self.assertEqual((s.emoji, s.keywords, s.summary), ("📰", [], "desc"))
        self.assertEqual(len(llm.calls), 2)

    def test_summarize_retry_can_succeed(self):
        llm = FakeLlm([{"emoji": "🐳"}, {"emoji": "🐳", "keywords": ["K"], "summary": "Second try."}])
        self.assertEqual(summarize(llm, "i", art("t"), "b").summary, "Second try.")

    def test_summarize_fallback_without_description(self):
        llm = FakeLlm([LlmError("a"), LlmError("b")])
        self.assertEqual(summarize(llm, "i", art("t"), "").summary, "Content unavailable.")


class DeepSeekTest(unittest.TestCase):
    def test_deepseek_error_hides_key(self):
        with mock.patch("openai.OpenAI") as client_cls:
            client_cls.return_value.chat.completions.create.side_effect = Exception("bad key sk-secret123")
            llm = DeepSeekLlm("sk-secret123", "https://api.deepseek.com", "deepseek-flash")
            with self.assertRaises(LlmError) as cm:
                llm.complete_json("s", "u")
        self.assertNotIn("sk-secret123", str(cm.exception))
        self.assertIn("***", str(cm.exception))

    def test_deepseek_sends_json_mode_and_model(self):
        with mock.patch("openai.OpenAI") as client_cls:
            create = client_cls.return_value.chat.completions.create
            create.return_value.choices = [mock.Mock(message=mock.Mock(content='{"ok": 1}'))]
            llm = DeepSeekLlm("k", "https://api.deepseek.com", "deepseek-flash")
            self.assertEqual(llm.complete_json("s", "u"), {"ok": 1})
        kwargs = create.call_args.kwargs
        self.assertEqual(kwargs["model"], "deepseek-flash")
        self.assertEqual(kwargs["response_format"], {"type": "json_object"})
        client_cls.assert_called_once_with(api_key="k", base_url="https://api.deepseek.com", timeout=120, max_retries=2)

    def test_deepseek_non_json_is_llm_error(self):
        with mock.patch("openai.OpenAI") as client_cls:
            client_cls.return_value.chat.completions.create.return_value.choices = [mock.Mock(message=mock.Mock(content="nope"))]
            with self.assertRaises(LlmError):
                DeepSeekLlm("k", "u", "m").complete_json("s", "u")


if __name__ == "__main__":
    unittest.main()
