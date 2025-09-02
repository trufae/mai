# Using Local Models

In today's landscape of AI and machine learning, running models locally on your own hardware offers several advantages, such as privacy, reduced latency, and more control over the computing environment. Here are insights into two frameworks you can utilize for deploying and working with large language models locally: Ollama and LMStudio.

## Ollama

The Ollama project is designed to facilitate the deployment and management of machine learning models locally. Using Ollama gives you the ability to handle specific models with greater ease, allowing you to customize and tweak them as needed without needing to rely on external resources or cloud-based services.

To run a model using Ollama, you can use the following command:

```console
mai -p ollama -m gemma3:12b -c newtools=true
```

- `-p ollama`: Specifies that we're using the Ollama platform.
- `-m gemma3:12b`: Indicates the model name and version. For example, `gemma3` version `12b`. This might be a hypothetical or custom-built model tailored to specific tasks.
- `-c newtools=true`: Configuration option to utilize new tools or capabilities within the Ollama framework. This could enable beta features or specific technical tools for model enhancement.

## LMStudio

LMStudio is a versatile local deployment solution that focuses on enhancing the performance and adaptability of language models by providing a customizable and powerful environment. It is specifically crafted for users who seek the ability to fine-tune large models on local hardware for various applications.

To run a model using LMStudio, use this command:

```console
mai -p openai -m openai/gpt-oss-20b -c newtools=true -c baseurl=http://192.168.1.39:1234/v1
```

- `-p openai`: Suggests that we're using the LMStudio platform with an OpenAI-like model interface.
- `-m openai/gpt-oss-20b`: Indicates the selected model, here potentially an open-source counterpart or version of OpenAI's GPT model with 20 billion parameters.
- `-c newtools=true`: Enables the utilization of new or experimental tools within the LMStudio setting.
- `-c baseurl=http://192.168.1.39:1234/v1`: Specifies the base URL for the model API, pointing to a machine's IP address and port where the model server is running. This parameter is used for directing local traffic to the appropriate service endpoint ensuring localized processing of requests.

Both of these platforms provide robust environments for leveraging advanced machine learning capabilities from the comfort of your own infrastructural setup. By handling models locally through Ollama and LMStudio, you can enjoy increased security, flexibility for experimentation, and optimized performance tailored to specific hardware constraints or application needs.
