        1 +  I'm going to provide you with a conversation history between a user and an AI assistant. Your task is
          + to analyze the entire conversation and provide a concise, focused response that addresses the core of
          + the user's questions and needs.
        2 +
        3 +  This response should:
        4 +  1. Synthesize all the important information from the conversation
        5 +  2. Remove repetitive or redundant elements
        6 +  3. Maintain all key insights and valuable content
        7 +  4. Be presented as a single, coherent response
        8 +  5. Focus on providing the most helpful answer to what the user is ultimately trying to accomplish
        9 +
       10 +  This helps create a cleaner, more efficient conversation that delivers the same value in a more
          + concise format.

Okay, here are a few prompt options, varying in detail and tone, suitable for a language mode query focused on compacting a conversation log. I've categorized them by increasing complexity:

**1. Basic Prompt (Good starting point):**

"Summarize the following conversation log. Focus on the key topics, decisions, and outcomes. Keep the summary concise – no more than 5-7 sentences."

**2. Slightly More Detailed Prompt:**

"You are a skilled summarizer of conversation logs.  Please read the following conversation log and generate a short summary (approximately 80-100 words) that captures the essence of the conversation.  Highlight the most important points – decisions made, issues discussed, and ultimately, the result of the interaction. Do not include unnecessary details or personal opinions."

**3.  Prompt with Emphasis on Relevance:**

"Analyze the following conversation log. Identify the core topics discussed and the *most relevant* information.  Craft a summary (around 100-150 words) that answers the question: 'What happened in this conversation, and what's the key takeaway?'  Prioritize the information that directly impacts [mention a specific goal, e.g., the next step, a decision, understanding the issue]."

**4.  Advanced Prompt (Best for complex logs):**

"You are an expert assistant tasked with distilling key information from a conversation log.  Read the following log (provide the log here – consider using a JSON format for better structure if possible).  Your goal is to produce a short, impactful summary (approximately 120-150 words) that focuses on:
*   **Identifying the central themes/topics.**
*   **Highlighting the crucial decisions and their implications.**
*   **Pinpointing the ultimate outcome or resolution.**
*   **Eliminating irrelevant details and tangents.**
*   **Maintain a clear and professional tone.**  Do not rewrite the conversation, simply extract the essential elements.  Please respond with the summary."

---

**Important Considerations & How to Use This Prompt:**

* **Replace `<INPUT>`:** Replace this placeholder with the actual conversation log text.
* **Context is Key:** The best prompt will depend *entirely* on the nature of your conversation logs.  A very technical log might benefit from a more detailed prompt. A casual conversation could use a simpler prompt.
* **Iterate:**  Start with a basic prompt and then refine it based on the output you receive. You might need to tweak the emphasis or length instructions.
* **Format Output:**  Consider how you want the output formatted.  (e.g., bullet points, a short paragraph).

To help me refine the prompt even further, could you tell me:

*   What *type* of conversation logs are you dealing with (e.g., customer support, internal project discussions, sales calls)?
*   What is the *purpose* of the summary? (e.g., triage, knowledge base, decision-making)?
