let currentSession = null;
let isStreaming = false;

// Initialize the interface
document.addEventListener('DOMContentLoaded', function() {
    loadConfig();
    loadSessions();
    checkStatus();

    // Add enter key handler for message input
    document.getElementById('message-input').addEventListener('keydown', function(e) {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            sendMessage();
        }
    });

    // Close sidebar when clicking outside on mobile
    document.addEventListener('click', function(e) {
        const sidebar = document.getElementById('sidebar');
        const menuToggle = document.querySelector('.menu-toggle');
        if (window.innerWidth <= 768 && sidebar.classList.contains('open') &&
            !sidebar.contains(e.target) && e.target !== menuToggle) {
            toggleSidebar();
        }
    });
});

// Check server status
async function checkStatus() {
    try {
        const response = await fetch('/health');
        const data = await response.json();
        const statusDiv = document.getElementById('status');
        if (data.server === 'running') {
            statusDiv.className = 'status online';
            statusDiv.textContent = 'MAI REPL Server Online';
        } else {
            statusDiv.className = 'status offline';
            statusDiv.textContent = 'MAI REPL Server Offline';
        }
    } catch (error) {
        document.getElementById('status').textContent = 'Cannot connect to MAI REPL server';
    }
}

// Load current configuration
async function loadConfig() {
    try {
        const response = await fetch('/api/config');
        const config = await response.json();

        document.getElementById('provider').value = config.provider || 'ollama';
        document.getElementById('stream').value = config.stream || 'true';
        document.getElementById('max_tokens').value = config.max_tokens || '5128';
        document.getElementById('temperature').value = config.temperature || '0.7';

        // Load models for the current provider
        await loadModelsForProvider(config.provider || 'ollama', config.model || 'gemma3:1b');
    } catch (error) {
        console.error('Failed to load config:', error);
    }
}

// Load models for a specific provider
async function loadModelsForProvider(provider, selectedModel = '') {
    const modelSelect = document.getElementById('model');

    // Clear existing options and show loading
    modelSelect.innerHTML = '<option value="">Loading models...</option>';
    modelSelect.disabled = true;

    try {
        const response = await fetch(`/api/models/${provider}`);

        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        const data = await response.json();

        if (data.models && data.models.length > 0) {
            // Clear loading option
            modelSelect.innerHTML = '';

            // Add model options
            data.models.forEach(model => {
                const option = document.createElement('option');
                option.value = model.id;
                option.textContent = model.name || model.id;
                if (model.id === selectedModel) {
                    option.selected = true;
                }
                modelSelect.appendChild(option);
            });

            // If no model was selected and we have models, select the first one
            if (!modelSelect.value && data.models.length > 0) {
                modelSelect.value = data.models[0].id;
            }
        } else {
            // No models available
            modelSelect.innerHTML = '<option value="">No models available for this provider</option>';
        }
    } catch (error) {
        console.error('Failed to load models:', error);
        modelSelect.innerHTML = `<option value="">Error: ${error.message}</option>`;
        addMessage('System', `Failed to load models for ${provider}: ${error.message}`, 'system');
    } finally {
        modelSelect.disabled = false;
    }
}

// Update provider and model
async function updateProvider() {
    const provider = document.getElementById('provider').value;
    const modelSelect = document.getElementById('model');
    const updateButton = document.querySelector('button[onclick="updateProvider()"]');
    const currentModel = modelSelect.value;

    // Disable button and show loading
    updateButton.disabled = true;
    updateButton.textContent = 'Updating...';

    try {
        // First load models for the new provider
        await loadModelsForProvider(provider, currentModel);

        // Get the selected model (might be different if the model wasn't available)
        const selectedModel = modelSelect.value;

        // Update the configuration
        await fetch('/api/config/set', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ provider, model: selectedModel })
        });
        addMessage('System', `Provider updated to ${provider}, model: ${selectedModel}`, 'system');
    } catch (error) {
        addMessage('System', `Failed to update provider: ${error.message}`, 'system');
    } finally {
        // Re-enable button
        updateButton.disabled = false;
        updateButton.textContent = 'Update Provider';
    }
}

// Update settings
async function updateSettings() {
    const stream = document.getElementById('stream').value;
    const max_tokens = document.getElementById('max_tokens').value;
    const temperature = document.getElementById('temperature').value;

    try {
        await fetch('/api/config/set', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ stream, max_tokens, temperature })
        });
        addMessage('System', 'Settings updated', 'system');
    } catch (error) {
        addMessage('System', 'Failed to update settings', 'system');
    }
}

// Load available sessions
async function loadSessions() {
    try {
        const response = await fetch('/api/sessions');
        const data = await response.json();
        // Sessions are logged to console, we'll implement a better way later
        console.log('Sessions loaded');
    } catch (error) {
        console.error('Failed to load sessions:', error);
    }
}

// Save current session
async function saveSession() {
    const name = document.getElementById('session-name').value || `session_${Date.now()}`;

    try {
        const response = await fetch('/api/session/save', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name })
        });
        const data = await response.json();
        addMessage('System', `Session saved as: ${data.name}`, 'system');
        document.getElementById('session-name').value = '';
    } catch (error) {
        addMessage('System', 'Failed to save session', 'system');
    }
}

// Send a message
async function sendMessage() {
    const input = document.getElementById('message-input');
    const message = input.value.trim();

    if (!message) return;

    // Add user message to chat
    addMessage('You', message, 'user');
    input.value = '';

    // Disable send button during processing
    const sendButton = document.getElementById('send-button');
    sendButton.disabled = true;
    sendButton.textContent = 'Sending...';

    try {
        const response = await fetch('/api/chat', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                message: message,
                stream: document.getElementById('stream').value === 'true'
            })
        });

        const data = await response.json();

        if (data.error) {
            addMessage('MAI', `Error: ${data.error}`, 'assistant');
        } else {
            addMessage('MAI', data.response, 'assistant');
        }
    } catch (error) {
        addMessage('MAI', `Network error: ${error.message}`, 'assistant');
    } finally {
        sendButton.disabled = false;
        sendButton.textContent = 'Send';
    }
}

// Toggle sidebar visibility on mobile
function toggleSidebar() {
    const sidebar = document.getElementById('sidebar');
    sidebar.classList.toggle('open');
}

// Add a message to the chat
function addMessage(sender, content, type) {
    const messagesDiv = document.getElementById('messages');
    const messageDiv = document.createElement('div');
    messageDiv.className = `message ${type}`;
    messageDiv.innerHTML = `<strong>${sender}:</strong> ${content.replace(/\n/g, '<br>')}`;
    messagesDiv.appendChild(messageDiv);
    messagesDiv.scrollTop = messagesDiv.scrollHeight;
}

// Auto-check status every 30 seconds
setInterval(checkStatus, 30000);