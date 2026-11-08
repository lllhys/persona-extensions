import { Row, Diff, View } from "@persona/extension/ui";
const badTitle = <Row key="bad" title={42} />;
const missingIdentity = <Row title="Synthetic" />;
const invented = <Diff key="bad-diff" path="a.ts" unsupported="invalid" />;
const html = <div>invalid guest DOM</div>;
const badView = <View key="bad-view" title={false} />;
void [badTitle, missingIdentity, invented, html, badView];
