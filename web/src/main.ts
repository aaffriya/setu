import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

const app = mount(App, { target: document.getElementById('app')! })

// Register the PWA service worker (app-shell cache). Service workers only run in
// a secure context (HTTPS or localhost); browsers block them on plain
// http://<lan-ip>, so we guard on isSecureContext and fail soft otherwise.
if ('serviceWorker' in navigator && window.isSecureContext) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/service-worker.js').catch((err) => {
      console.warn('service worker registration failed:', err)
    })
  })
}

export default app
