# THIRD_PARTY_NOTICES

OpsCockpit embeds and adapts code from the following third-party projects.

## Homelable

- **Project:** [Pouzor/homelable](https://github.com/Pouzor/homelable)
- **License:** MIT (see `LICENSE.homelable` in this repository)
- **Upstream commit:** `f07f43686ec05f586bebe476b889a47137d2af2d` (v3.1.2)
- **Used for:** the frontend topology engine — React Flow (`@xyflow/react`), Dagre
  auto-layout, the node/edge component pattern, and reusable UI components.

See `UPSTREAM.md` for the full reuse/modification inventory.

### MIT License — Homelable

Copyright (c) 2026 Remy Jardinet (see `LICENSE.homelable`)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Direct npm dependencies (bundled into the frontend build)

The following third-party packages are used by the frontend and are subject to
their own licenses (typically MIT). They are not vendored in this repository;
they are resolved by `npm install` at build time.

| Package | License |
|---|---|
| @xyflow/react | MIT |
| @dagrejs/dagre | MIT |
| dagre | MIT |
| react / react-dom | MIT |
| zustand | MIT |
| lucide-react | ISC |
| vite | MIT |
| typescript | Apache-2.0 |
| vitest | MIT |
| tailwindcss | MIT |
| js-yaml | MIT |
| clsx / tailwind-merge / class-variance-authority | MIT |

For the complete dependency tree and license texts, see `frontend/package-lock.json`
and the `node_modules` directory after `npm install`.
