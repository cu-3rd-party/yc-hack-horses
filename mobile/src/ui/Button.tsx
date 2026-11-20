import React from 'react';
import { StyleSheet, Text } from 'react-native';
import { pressable } from './pressable';

// Общая кнопка без внешних зависимостей.
export function Button(props: { title: string; onPress: () => void; disabled?: boolean }) {
  return (
    <Text
      style={[styles.base, props.disabled && styles.disabled]}
      onPress={props.disabled ? undefined : props.onPress}
      {...pressable()}
    >
      {props.title}
    </Text>
  );
}

const styles = StyleSheet.create({
  base: {
    backgroundColor: '#1f6feb',
    color: '#fff',
    paddingVertical: 12,
    paddingHorizontal: 16,
    borderRadius: 8,
    textAlign: 'center',
    fontWeight: '600',
    overflow: 'hidden',
  },
  disabled: { opacity: 0.5 },
});
