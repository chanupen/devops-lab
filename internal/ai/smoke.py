# The ai stack's smoke test, run as a Job in the ai namespace (labelled
# lab.devops/ollama=true, so the NetworkPolicy lets it reach Ollama).
# It asks the model one question through ollama.ai.svc, sends one
# OpenTelemetry span (a Langfuse generation) to Langfuse over OTLP/HTTP,
# and prints one JSON line with the answer and the timings. Standard
# library only.
import base64, json, os, secrets, time, urllib.request

OLLAMA = os.environ.get("OLLAMA_URL", "http://ollama.ai.svc:11434")
LANGFUSE = os.environ.get("LANGFUSE_URL", "http://langfuse-web.ai.svc:3000")
MODEL = os.environ["MODEL"]
QUESTION = os.environ["QUESTION"]


def post(url, body, headers=None, timeout=300):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json", **(headers or {})})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.status, json.loads(r.read() or b"{}")


start = time.time_ns()
_, gen = post(OLLAMA + "/api/generate", {"model": MODEL, "prompt": QUESTION, "stream": False,
                                         "options": {"temperature": 0, "seed": 1, "num_predict": 128}})
end = time.time_ns()
answer = gen.get("response", "").strip()
eval_count, eval_ns = gen.get("eval_count", 0), gen.get("eval_duration", 0)
result = {
    "model": MODEL,
    "answer": answer,
    "latencySeconds": round((end - start) / 1e9, 3),
    "loadSeconds": round(gen.get("load_duration", 0) / 1e9, 3),
    "promptTokens": gen.get("prompt_eval_count", 0),
    "promptTokensPerSecond": round(gen.get("prompt_eval_count", 0) / (gen.get("prompt_eval_duration", 0) / 1e9), 1)
    if gen.get("prompt_eval_duration") else 0,
    "outputTokens": eval_count,
    "outputTokensPerSecond": round(eval_count / (eval_ns / 1e9), 1) if eval_ns else 0,
}

trace_id, span_id = secrets.token_hex(16), secrets.token_hex(8)


def attr(k, v):
    if isinstance(v, int):
        return {"key": k, "value": {"intValue": str(v)}}
    return {"key": k, "value": {"stringValue": str(v)}}


span = {"resourceSpans": [{
    "resource": {"attributes": [attr("service.name", "lab-ai-smoke")]},
    "scopeSpans": [{"scope": {"name": "lab-ai-smoke"}, "spans": [{
        "traceId": trace_id, "spanId": span_id, "name": "smoke-question", "kind": 3,
        "startTimeUnixNano": str(start), "endTimeUnixNano": str(end),
        "attributes": [
            attr("langfuse.observation.type", "generation"),
            attr("langfuse.observation.input", QUESTION),
            attr("langfuse.observation.output", answer),
            attr("gen_ai.system", "ollama"),
            attr("gen_ai.request.model", MODEL),
            attr("gen_ai.usage.input_tokens", result["promptTokens"]),
            attr("gen_ai.usage.output_tokens", eval_count),
        ]}]}]}]}
auth = base64.b64encode(f"{os.environ['LANGFUSE_PUBLIC_KEY']}:{os.environ['LANGFUSE_SECRET_KEY']}".encode()).decode()
status, _ = post(LANGFUSE + "/api/public/otel/v1/traces", span, {"Authorization": "Basic " + auth}, timeout=30)
result["traceId"], result["traceStatus"] = trace_id, status
print(json.dumps(result), flush=True)
