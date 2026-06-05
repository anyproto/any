// miniapp is dataset-backed (built-in `Mini App` type, `mini_app` dataset)
// rather than markdown-block-parsed. Exercises the full lifecycle against a
// live server. miniapp reads API config from env (the runner's .env), which
// points at the same server/space as the test. Run via jsrunner_test.go.

import {
  createMiniApp, updateMiniApp, editMiniApp, setState, getState,
  getMiniApp, getMiniAppSource, upsertReadme, listMiniApps
} from "miniapp@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}

export function main(args) {
  var h = mkHarness();
  var name = "app_" + new Date().getTime();
  var source = "<div id=root></div>\n<script>ReactDOM.render('hi', root)</script>";

  // create
  var c = createMiniApp({ name: name, source: source, readme: "# Readme", state: { n: 1 } });
  h.check("create ok", c && c.ok, JSON.stringify(c));
  // runtime scripts auto-injected (react/react-dom/hook missing in source)
  h.check("create injected runtime scripts", c && c.warnings && c.warnings.length === 1, JSON.stringify(c && c.warnings));

  // get — source/state/readme round-trip via dataset (no markdown surgery)
  var app = getMiniApp(name);
  h.check("get returns app", !!app, JSON.stringify(app));
  h.check("get source contains author markup", app && app.source.indexOf("ReactDOM.render('hi'") !== -1, app && app.source);
  h.check("get source has injected react.js", app && app.source.indexOf('src="./react.js"') !== -1);
  h.check("get state parsed", app && app.state && app.state.n === 1, JSON.stringify(app && app.state));
  h.check("get readme", app && app.readme === "# Readme", app && app.readme);

  // getState
  var st = getState(name);
  h.check("getState", st && st.n === 1, JSON.stringify(st));

  // setState — atomic, must not touch source
  var ss = setState(name, { n: 2, ok: true });
  h.check("setState ok", ss && ss.ok, JSON.stringify(ss));
  var app2 = getMiniApp(name);
  h.check("setState updated state", app2 && app2.state.n === 2, JSON.stringify(app2 && app2.state));
  h.check("setState left source intact", app2 && app2.source.indexOf("ReactDOM.render('hi'") !== -1);

  // editMiniApp — string replace in source
  var ed = editMiniApp(name, { oldString: "'hi'", newString: "'bye'" });
  h.check("edit ok", ed && ed.ok, JSON.stringify(ed));
  h.check("edit applied", getMiniAppSource(name).source.indexOf("'bye'") !== -1);

  // updateMiniApp — replace readme only
  var up = upsertReadme(name, "# New Readme");
  h.check("upsertReadme ok", up && up.ok, JSON.stringify(up));
  h.check("readme updated", getMiniApp(name).readme === "# New Readme");

  // updateMiniApp source
  var up2 = updateMiniApp({ name: name, source: "<p>v2</p>" });
  h.check("updateMiniApp ok", up2 && up2.ok, JSON.stringify(up2));
  h.check("source replaced", getMiniAppSource(name).source.indexOf("<p>v2</p>") !== -1);
  h.check("state survived source update", getState(name).n === 2);

  // list
  var list = listMiniApps();
  var found = false;
  for (var i = 0; i < list.length; i++) { if (list[i].name === name) { found = true; break; } }
  h.check("listMiniApps includes our app", found, JSON.stringify(list));

  // duplicate create rejected
  var dup = createMiniApp({ name: name, source: "x" });
  h.check("duplicate create rejected", dup && dup.ok === false, JSON.stringify(dup));

  return h.done();
}
