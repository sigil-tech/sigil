import { useState } from 'preact/hooks';

interface EditableListProps {
  items: string[];
  onChange: (items: string[]) => void;
  placeholder?: string;
}

export function EditableList({ items, onChange, placeholder }: EditableListProps) {
  const [newItem, setNewItem] = useState('');

  const add = () => {
    const val = newItem.trim();
    if (val && !items.includes(val)) {
      onChange([...items, val]);
      setNewItem('');
    }
  };

  const remove = (index: number) => {
    onChange(items.filter((_, i) => i !== index));
  };

  return (
    <div class="editable-list">
      {items.map((item, i) => (
        <div class="editable-list-item" key={i}>
          <span>{item}</span>
          <button class="remove-btn" onClick={() => remove(i)}>x</button>
        </div>
      ))}
      <div class="editable-list-add">
        <input
          type="text"
          value={newItem}
          onInput={(e) => setNewItem((e.target as HTMLInputElement).value)}
          onKeyDown={(e) => e.key === 'Enter' && add()}
          placeholder={placeholder || 'Add item...'}
        />
        <button class="add-btn" onClick={add}>+</button>
      </div>
    </div>
  );
}
