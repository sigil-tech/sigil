import { render, fireEvent } from '@testing-library/preact';
import { describe, it, expect, vi } from 'vitest';
import { AppRail } from './AppRail';

describe('AppRail', () => {
  it('renders primary and secondary navigation items', () => {
    const onSelect = vi.fn();
    const { getByLabelText } = render(
      <AppRail activeView="list" onSelect={onSelect} />
    );
    expect(getByLabelText('Suggestions')).toBeTruthy();
    expect(getByLabelText('VM Launcher')).toBeTruthy();
    expect(getByLabelText('Audit')).toBeTruthy();
    expect(getByLabelText('Settings')).toBeTruthy();
  });

  it('marks the active view with aria-current', () => {
    const onSelect = vi.fn();
    const { getByLabelText } = render(
      <AppRail activeView="vm" onSelect={onSelect} />
    );
    const vmBtn = getByLabelText('VM Launcher');
    expect(vmBtn.getAttribute('aria-current')).toBe('page');
    const listBtn = getByLabelText('Suggestions');
    expect(listBtn.getAttribute('aria-current')).toBeNull();
  });

  it('calls onSelect when a rail button is clicked', () => {
    const onSelect = vi.fn();
    const { getByLabelText } = render(
      <AppRail activeView="list" onSelect={onSelect} />
    );
    fireEvent.click(getByLabelText('VM Launcher'));
    expect(onSelect).toHaveBeenCalledWith('vm');
  });

  it('applies the active class to the active button', () => {
    const onSelect = vi.fn();
    const { getByLabelText } = render(
      <AppRail activeView="audit" onSelect={onSelect} />
    );
    const auditBtn = getByLabelText('Audit');
    expect(auditBtn.classList.contains('app-rail__btn--active')).toBe(true);
  });
});
