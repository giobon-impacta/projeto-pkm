export async function requestJSON(url, options = {}, onUnauthorized = () => globalThis.location.replace('/login.html')) {
    const response = await fetch(url, {
        ...options,
        headers: {
            ...(options.body ? { 'Content-Type': 'application/json' } : {}),
            ...options.headers
        }
    });
    if (!response.ok) {
        const text = (await response.text()).trim();
        let body = null;
        if (text) {
            try {
                body = JSON.parse(text);
            } catch {
                body = null;
            }
        }
        if (response.status === 401) onUnauthorized();
        const error = new Error(body?.error || text || `HTTP ${response.status}`);
        error.status = response.status;
        error.body = body;
        throw error;
    }
    if (response.status === 204) return null;
    return response.json();
}
