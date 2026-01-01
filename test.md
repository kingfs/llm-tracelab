# LLM Proxy Test

## list models

```bash
curl http://localhost:8080/v1/models | jq 
```

## non-stream

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-14b", "messages":[{"role":"user","content":"1+1=?,Just answer with a number, no explanation."}], "max_completion_tokens":4096}'
  
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"给初中生讲明白大模型原理"}],"max_completion_tokens":8192}'

curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-r1","messages":[{"role":"user","content":"ping"}],"max_completion_tokens":8192}'

curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-r1","messages":[{"role":"user","content":"ping"}],"max_completion_tokens":5}'

curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"用5个字介绍一下自己"}],"max_completion_tokens":10}'
```

## stream

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-r1","messages":[{"role":"user","content":"给初中生讲明白大模型原理"}],"max_completion_tokens":8192, "stream": true, "stream_options": {"include_usage": true}}'
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"给初中生讲明白大模型原理"}],"max_completion_tokens":8192, "stream": true, "stream_options": {"include_usage": true}}'

curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"讲一个20字笑话"}],"max_completion_tokens":128, "stream": true, "stream_options": {"include_usage": true}}'
# 不存在则增加"stream_options": {"include_usage": true}
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-14b","messages":[{"role":"user","content":"讲一个20字笑话"}],"max_completion_tokens":1024, "stream": true}'

curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"system","content":"你擅长模仿蜡笔小新对话\n注意:你是一个幼儿园学生\n你喜欢搭讪"},{"role":"user","content":"你好！\n小哥哥～"}],"max_completion_tokens":1024, "stream": true}'
```
