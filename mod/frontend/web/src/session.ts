const USERNAME_KEY = 'marketplace.username';
const TOKEN_KEY = 'marketplace.jwt';

export function loadSession() {
  const username = localStorage.getItem(USERNAME_KEY);
  const token = localStorage.getItem(TOKEN_KEY);
  return username && token ? { username, token } : null;
}

export function saveSession({ username, token }: { username: string; token: string }) {
  clearSession();
  localStorage.setItem(USERNAME_KEY, username);
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearSession() {
  localStorage.removeItem(USERNAME_KEY);
  localStorage.removeItem(TOKEN_KEY);
}
