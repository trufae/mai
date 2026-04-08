# OpenAI's tiktoken (pip install tiktoken)
echo "hello world" | python3 -c "import sys,tiktoken;e=tiktoken.get_encoding('cl100k_base');print(len(e.encode(sys.stdin.read())))"

# Anthropic API - count tokens via the API
curl -s https://api.anthropic.com/v1/messages/count_tokens \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-6-20250514","messages":[{"role":"user","content":"your text here"}]}' | jq .input_tokens

# OpenAI API
curl -s https://api.openai.com/v1/chat/completions \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"your text here"}],"max_tokens":1}' | jq .usage.prompt_tokens

