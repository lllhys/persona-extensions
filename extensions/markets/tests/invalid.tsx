import { Button, Metric, View } from "@persona/extension/ui";
export const badType = <Button key="fixture" title="fixture" unsupported="invalid" />;
export const badValue = <Metric key="metric" label="fixture" value={42} />;
export const missingKey = <View />;
