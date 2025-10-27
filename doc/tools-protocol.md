At the **JSON/HTTP protocol level**, *tool calling* (also known as *function calling* or *structured tool invocation*) works as a standardized message exchange pattern between an **LLM runtime (the model API)** and a **controller or agent framework** that coordinates *tools* (external APIs, scripts, or functions).  

Let’s break it down step by step:

---

### 🧩 1. The setup
There are three actors:
- **Client (Agent/Orchestrator):** sends prompts and receives structured responses.  
- **Model API (LLM):** interprets text and decides when a “tool” is needed.  
- **Tool server or process:** an external function/API the model can call via JSON.

They all communicate using JSON over HTTP.

---

### 🧠 2. The request to the model
A typical `POST /v1/chat/completions` request includes:
```json
{
  "model": "gpt-5",
  "messages": [
    {"role": "system", "content": "You can use the 'weather' tool to fetch forecasts."},
    {"role": "user", "content": "What's the weather in Barcelona?"}
  ],
  "tools": [
    {
      "name": "weather",
      "description": "Get weather info for a city",
      "parameters": {
        "type": "object",
        "properties": {
          "city": {"type": "string"}
        },
        "required": ["city"]
      }
    }
  ],
  "tool_choice": "auto"
}
```

---

### ⚙️ 3. The model’s response: a *tool call*
Instead of returning a normal message, the model outputs a **structured JSON block** indicating which tool to call and with what arguments:

```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1725828825,
  "model": "gpt-5",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "tool_calls": [
          {
            "id": "call_weather_1",
            "type": "function",
            "function": {
              "name": "weather",
              "arguments": "{\"city\": \"Barcelona\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ]
}
```

At this point, the model is saying:
> “I want to call the `weather` tool with `{ city: 'Barcelona' }`.”

---

### 🔁 4. The agent executes the tool
The **agent/controller** receives this JSON, extracts the function name and arguments, then performs the tool call — usually another HTTP request or local function call.

Example:
```json
POST /tools/weather
{
  "city": "Barcelona"
}
```

And the **tool replies**:
```json
{
  "temperature": 22,
  "condition": "sunny"
}
```

---

### 🧩 5. The agent sends the tool result back to the model
Now the agent must feed that result back into the model to let it reason and produce a final answer.  
So it sends a *new chat completion*:

```json
{
  "model": "gpt-5",
  "messages": [
    {"role": "system", "content": "You can use the 'weather' tool to fetch forecasts."},
    {"role": "user", "content": "What's the weather in Barcelona?"},
    {
      "role": "assistant",
      "tool_calls": [
        {
          "id": "call_weather_1",
          "type": "function",
          "function": {
            "name": "weather",
            "arguments": "{\"city\": \"Barcelona\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_weather_1",
      "content": "{\"temperature\":22,\"condition\":\"sunny\"}"
    }
  ]
}
```

---

### 💬 6. The final model response
Now the model replies with the *final natural-language message*:

```json
{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "It's 22°C and sunny in Barcelona right now."
      },
      "finish_reason": "stop"
    }
  ]
}
```

---

### 🔍 7. Protocol summary

| Stage | Direction | Content Type | Purpose |
|--------|------------|---------------|----------|
| 1 | Client → Model | JSON | Ask question & declare tools |
| 2 | Model → Client | JSON (tool_call) | Request tool execution |
| 3 | Client → Tool | JSON | Execute tool with given args |
| 4 | Tool → Client | JSON | Return tool output |
| 5 | Client → Model | JSON | Provide tool result |
| 6 | Model → Client | JSON (content) | Final answer |

---

### 🌐 HTTP design notes
- Each of these JSON payloads travels over **HTTP POST** with `Content-Type: application/json`.
- Streaming (`text/event-stream`) can also be used so that the model’s tool call arrives incrementally.
- The model doesn’t directly talk to the tool; the **controller** orchestrates all steps.

---

Would you like me to show you a **real raw HTTP transcript** (with request/response headers and bodies) as it happens between client and model — for example, how OpenAI’s `tool_calls` or Anthropic’s `function_call` messages look on the wire?
