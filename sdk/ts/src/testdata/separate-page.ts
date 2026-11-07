import { componentPage } from "../index.ts";
import { createElement, View, Section, Text, Button, callback } from "../components.ts";
import { useState } from "../hooks.ts";

export default {
  id: "separate-bundle", version: "1",
  ui: {home: componentPage(() => createElement(Home, {key: "page"}))},
};
function Home() {
  const [count, setCount] = useState(0);
  return createElement(View, {key: "root", children: createElement(Section, {key: "main", children: [
    createElement(Text, {key: "value", text: String(count)}),
    createElement(Button, {key: "change", title: "Change", action: callback(() => {setCount(count + 1); return {toast: "Saved"};})}),
  ]})});
}
