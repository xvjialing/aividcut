import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import './styles/tokens.css';
import './styles/app.css';

// 原生标题栏控件占位随平台设置，普通浏览器不预留macOS交通灯空间。
document.documentElement.dataset.nativePlatform = window._wails?.environment?.OS || 'browser';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
