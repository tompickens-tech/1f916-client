import re
import os

css_path = 'web/static/style.css'
with open(css_path, 'r') as f:
    css = f.read()

# 1. Update :root variables for a modern vibrant look
root_vars = """
:root {
  color-scheme: light dark;
  
  /* Modern Vibrant Colors */
  --paper: #f0f2f5;
  --sunk: #e4e6eb;
  --raised: rgba(255, 255, 255, 0.7);
  
  /* Ink */
  --ink: #1c1e21;
  --ink-2: #606770;
  --ink-3: #8d949e;
  
  /* Rules */
  --rule: rgba(0, 0, 0, 0.1);
  --rule-soft: rgba(0, 0, 0, 0.05);
  --rule-firm: rgba(0, 0, 0, 0.2);
  
  /* Signal */
  --link: #1877f2;
  --link-visit: #7b4ee4;
  --flag: #e41e3f;
  --flag-bg: rgba(228, 30, 63, 0.1);
  --hold: #f5a623;
  --hold-bg: rgba(245, 166, 35, 0.1);
  --ok: #31a24c;
  --ok-bg: rgba(49, 162, 76, 0.1);
  
  /* Type */
  --sans: 'Inter', ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  --mono: ui-monospace, "SFMono-Regular", "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
  
  /* Spacing */
  --s1: 4px;
  --s2: 8px;
  --s3: 12px;
  --s4: 16px;
  --s6: 24px;
  --s8: 32px;
  --s12: 48px;
  
  --measure: 40rem;
  --page: 64rem;
  
  /* Gradients */
  --bg-gradient: linear-gradient(135deg, #fdfbfb 0%, #ebedee 100%);
}

@media (prefers-color-scheme: dark) {
  :root {
    --paper: #18191a;
    --sunk: #242526;
    --raised: rgba(36, 37, 38, 0.7);
    --ink: #e4e6eb;
    --ink-2: #b0b3b8;
    --ink-3: #8a8d91;
    --rule: rgba(255, 255, 255, 0.1);
    --rule-soft: rgba(255, 255, 255, 0.05);
    --rule-firm: rgba(255, 255, 255, 0.2);
    --link: #4facfe;
    --link-visit: #a18cd1;
    --flag: #fa5c7c;
    --flag-bg: rgba(250, 92, 124, 0.15);
    --hold: #ffce67;
    --hold-bg: rgba(255, 206, 103, 0.15);
    --ok: #0acf97;
    --ok-bg: rgba(10, 207, 151, 0.15);
    
    --bg-gradient: linear-gradient(135deg, #1f1c2c 0%, #928dab 100%);
  }
}
"""

css = re.sub(r':root \{.*?\}(?=\s*/\* --- 2\. Base)', root_vars, css, flags=re.DOTALL)

# 2. Add Animated Gradient Background to Body
body_replacement = """body {
  margin: 0;
  background: var(--bg-gradient);
  background-size: 200% 200%;
  animation: gradientBG 15s ease infinite;
  color: var(--ink);
  font-family: var(--sans);
  font-size: 16px;
  line-height: 1.55;
  font-variant-numeric: tabular-nums;
}

@keyframes gradientBG {
  0% { background-position: 0% 50%; }
  50% { background-position: 100% 50%; }
  100% { background-position: 0% 50%; }
}
"""
css = re.sub(r'body \{[^}]+\}', body_replacement, css)

# 3. Add Glassmorphism and Hover Micro-Animations to components
glass_css = """
  background: var(--raised);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  border: 1px solid var(--rule-soft);
  border-radius: 12px;
  box-shadow: 0 4px 6px rgba(0, 0, 0, 0.05);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
"""

css = re.sub(r'(\.post-card\s*\{[^}]*?)background: var\(--raised\);', r'\1' + glass_css, css)
css = re.sub(r'(\.well\s*\{[^}]*?)background: var\(--raised\);', r'\1' + glass_css, css)
css = re.sub(r'(\.session-bar\s*\{[^}]*?)background: var\(--sunk\);', r'\1' + glass_css, css)

# Make app-header glass
header_glass = """
  background: var(--raised);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  border-bottom: 1px solid var(--rule-soft);
  position: sticky;
  top: 0;
  z-index: 100;
"""
css = re.sub(r'(\.app-header\s*\{[^}]*?)background: var\(--paper\);', r'\1' + header_glass, css)

# Update post-card hover
css = css + """
.post-card:hover {
  transform: translateY(-4px);
  box-shadow: 0 10px 15px rgba(0, 0, 0, 0.1);
}

.btn {
  transition: all 0.2s ease !important;
  border-radius: 8px !important;
  font-weight: 500 !important;
}

.btn:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
}

.brand {
  transition: color 0.2s ease;
}

.brand:hover {
  color: var(--link);
}
"""

# Button radius
css = re.sub(r'border-radius: 3px;', 'border-radius: 8px;', css)

with open(css_path, 'w') as f:
    f.write(css)

print("UI enhancements applied.")
