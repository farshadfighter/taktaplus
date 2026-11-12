# Generates the OpenAPI route table from a chi server.go.
# usage: routes.py SERVER_GO API_DIR PKG OUT RECEIVER
import re, glob, sys
server, apidir, pkg, out = sys.argv[1:5]
src = open(server).read()

funcs = {}
for f in glob.glob(apidir + '/*.go'):
    if f.endswith('_test.go'): continue
    s = open(f).read()
    for m in re.finditer(r'\nfunc \(s \*srv\) (\w+)\(', s):
        start = m.end(); nxt = s.find('\nfunc ', start)
        funcs[m.group(1)] = s[start:nxt if nxt > 0 else len(s)]
reauth = {k for k, v in funcs.items() if 's.reauth(' in v}

perms = {}      # var -> perm const
panelvars = set()
routes = []
lines = src.split('\n')
loop = None
for ln in lines:
    l = ln.strip()
    m = re.match(r'(\w+) := requirePerm\(auth\.(\w+)\)', l)
    if m: perms[m.group(1)] = m.group(2); continue
    m = re.match(r'(\w+) := func\(h http\.Handler\) http\.Handler \{ return panelOnly\(requirePerm\(auth\.(\w+)\)\(h\)\) \}', l)
    if m: perms[m.group(1)] = m.group(2); panelvars.add(m.group(1)); continue
    m = re.match(r'for _, a := range \[\]string\{(.*)\} \{', l)
    if m: loop = re.findall(r'"(\w+)"', m.group(1)); continue
    m = re.match(r'r(?:\.With\(([^)]*\)?)\))?\.(Get|Post|Put|Delete|Patch)\("([^"]*)"(\+a)?, ?(.*)', l)
    if not m: continue
    mw, method, path, plus, handler = m.groups()
    hm = re.match(r's\.(\w+)', handler); pm = re.match(r'pr\["(\w+)"\]', handler)
    hname = hm.group(1) if hm else (pm.group(1) if pm else ('version' if 'version' in path else 'inline'))
    perm = ''; access = 'Session'
    if mw is None:
        access = 'Public'
    else:
        mw = mw.rstrip(')') if mw.count(')') > mw.count('(') else mw
        mm = re.match(r'requirePerm\(auth\.(\w+)', mw)
        if mm: perm = mm.group(1)
        elif mw in perms: perm = perms[mw]
        if mw in panelvars: access = 'PanelOnly'
    if hname in reauth: access = 'PanelOnly'
    paths = [(path + a, a) for a in loop] if plus else [(path, None)]
    for p, a in paths:
        h = a if a else hname
        routes.append((method.upper(), p, h, perm, access))
    if plus: loop = None

def words(name):
    if name == 'openAPI': return 'OpenAPI document'
    name = re.sub(r'^handle', '', name)
    name = {'MFA': 'verify MFA code', 'MFASetup': 'start MFA setup', 'MFAEnable': 'enable MFA', 'Me': 'current user', 'Login': 'log in', 'Logout': 'log out', 'AuditList': 'list audit log', 'openAPI': 'OpenAPI document'}.get(name, name)
    w = re.sub(r'([a-z0-9])([A-Z])', r'\1 \2', name).replace('_', ' ').lower()
    return w[:1].upper() + w[1:]

with open(out, 'w') as f:
    f.write('// Code generated from server.go by routes.py; keep it in step with the\n// router (TestOpenAPICoversRoutes checks it). Summaries may be edited.\n\n')
    f.write('package %s\n\nimport "github.com/farshadfighter/taktaplus-lic/pkg/apidoc"\n\n' % pkg)
    f.write('var apiRoutes = []apidoc.Route{\n')
    for method, p, h, perm, access in routes:
        if p == '/healthz': continue
        tag = p.strip('/').split('/')[0].split('.')[0] or 'system'
        permv = ('string(auth.%s)' % perm) if perm else '""'
        f.write('\t{Method: "%s", Path: "%s", Tag: "%s", Summary: "%s", Perm: %s, Access: apidoc.%s},\n' % (method, p, tag, words(h), permv, access))
    f.write('}\n')
print(len(routes), 'routes;', len(reauth), 'panel-only handlers')
