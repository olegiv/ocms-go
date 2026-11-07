// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The package ships these test exports from the same parser used by cdn.min.js.
const source = readFileSync('node_modules/@alpinejs/csp/src/parser.js');
const { Parser, Tokenizer } = await import('data:text/javascript;base64,' + source.toString('base64'));
const files = JSON.parse(readFileSync(0, 'utf8'));
const components = new Map();
const magics = new Set(['$el', '$refs', '$store', '$watch', '$dispatch', '$nextTick', '$root', '$data', '$id']);
const errors = [];
let checked = 0;

for (const file of files) {
    for (const script of file.scripts || []) {
        const callbacks = [];
        const emptyElement = { addEventListener() {}, appendChild() {} };
        const environment = {
            Alpine: {
                data(name, provider) {
                    components.set(name, Object.keys(provider()));
                },
            },
            document: {
                addEventListener(name, callback) {
                    if (name === 'alpine:init') callbacks.push(callback);
                },
                getElementById() { return null; },
                querySelector() { return null; },
                querySelectorAll() { return []; },
                createElement() { return {}; },
                body: emptyElement,
                head: emptyElement,
                documentElement: { dataset: {} },
            },
            addEventListener() {},
            localStorage: { getItem() { return null; } },
        };
        environment.window = environment;
        try {
            vm.runInNewContext(script, environment, { filename: file.path, timeout: 500 });
            for (const callback of callbacks) callback();
        } catch (error) {
            errors.push(`${file.path}: reading Alpine.data registrations: ${error.message}`);
        }
    }
}

function verifyNames(node, scope) {
    if (!node || typeof node !== 'object') return;
    if (node.type === 'Identifier') {
        if (!scope.has(node.name)) throw new Error('undefined scope identifier: ' + node.name);
        return;
    }
    for (const [key, value] of Object.entries(node)) {
        // Plain property names are looked up on the receiver, not the scope.
        if (!node.computed && ((node.type === 'MemberExpression' && key === 'property') ||
            (node.type === 'Property' && key === 'key'))) continue;
        if (Array.isArray(value)) value.forEach(item => verifyNames(item, scope));
        else verifyNames(value, scope);
    }
}

function check(file, attr, value, scope) {
    try {
        const ast = new Parser(new Tokenizer(value).tokenize()).parse();
        verifyNames(ast, scope);
        checked++;
        return ast;
    } catch (error) {
        errors.push(`${file}: ${attr}="${value}": ${error.message}`);
    }
}

function walk(file, element, parent) {
    const attrs = element.attrs || {};
    const scope = new Set(parent);
    const data = attrs['x-data'];
    if (data) {
        const ast = check(file, 'x-data', data, new Set([...scope, ...components.keys()]));
        const name = ast?.type === 'CallExpression' ? ast.callee.name : ast?.name;
        if (name && components.has(name)) {
            for (const member of components.get(name)) scope.add(member);
        }
        if (ast?.type === 'ObjectExpression') {
            for (const { key, computed } of ast.properties) {
                if (!computed) scope.add(key.name ?? key.value);
            }
        }
    }
    const loop = attrs['x-for'];
    if (loop) {
        const match = loop.match(/^(.+?)\s+(?:in|of)\s+(.+)$/s);
        if (!match) errors.push(`${file}: unsupported x-for="${loop}"`);
        else {
            check(file, 'x-for', match[2], scope);
            for (const name of match[1].replace(/^\(|\)$/g, '').split(',')) scope.add(name.trim());
        }
    }
    for (const [attr, value] of Object.entries(attrs)) {
        // These directives take literal names, selectors, classes or flags.
        if (!value || /^(?:x-(?:data|for|ref|teleport|transition|cloak|ignore)(?:[.:]|$)|x-sort:group$)/.test(attr)) continue;
        const locals = new Set(scope);
        if (attr.startsWith('@') || attr.startsWith('x-on:')) locals.add('$event');
        if (attr === 'x-sort') {
            locals.add('$item');
            locals.add('$position');
        }
        check(file, attr, value, locals);
    }
    for (const child of element.children || []) walk(file, child, scope);
}

// These partials inherit state from a caller in another template. Keep the
// explicit bindings narrow so unrelated components cannot satisfy a name.
const callerScopes = new Map([
    ['internal/views/admin/header.templ:Header', 'adminLayout'],
    ['internal/views/admin/sidebar.templ:Sidebar', 'adminLayout'],
    ['internal/views/admin/modules.templ:activationPasswordDialog', 'moduleToggle'],
    ['internal/views/admin/pages.templ:mediaDropzoneSelectArea', 'mediaDropzone'],
    ['internal/views/admin/pages.templ:mediaDropzoneSelectModal', 'mediaDropzone'],
]);
const legacyAdminPartials = new Set(['web/templates/partials/header.html', 'web/templates/partials/sidebar.html']);
for (const file of files) {
    for (const root of file.roots || []) {
        const inherited = callerScopes.get(file.path + ':' + (root.template || ''));
        const scope = new Set([...magics, ...(components.get(inherited) || [])]);
        if (legacyAdminPartials.has(file.path)) scope.add('sidebarOpen');
        walk(file.path, root, scope);
    }
}
if (errors.length) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
} else {
    console.log(`Checked ${checked} Alpine expressions against ${components.size} registered components`);
}
