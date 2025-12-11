<script lang="ts" setup>
import { type Item } from "@/stores/workspace";
import { ref } from "vue";

const props = defineProps<{
  item: Item;
}>();

const emit = defineEmits<{
  addItem: [Item, Item];
  removeItem: [Item, number];
  renameItem: [Item, string];
}>();

const newItemName = ref("");
const isAddingFile = ref(false);
const isAddingFolder = ref(false);
const isOpen = ref(true); // Default open

const startAddFile = () => {
  isAddingFile.value = true;
  isAddingFolder.value = false;
  newItemName.value = "";
};

const startAddFolder = () => {
  isAddingFolder.value = true;
  isAddingFile.value = false;
  newItemName.value = "";
};

const cancelAdd = () => {
  isAddingFile.value = false;
  isAddingFolder.value = false;
  newItemName.value = "";
};

const confirmAdd = () => {
  if (newItemName.value.trim()) {
    const type = isAddingFile.value ? "grpc" : "folder";
    emit("addItem", props.item, { name: newItemName.value, type: type });
    isOpen.value = true; // Ensure open when adding
  }
  cancelAdd();
};

const toggleOpen = () => {
  if (props.item.type === "folder") {
    isOpen.value = !isOpen.value;
  }
};

const handleRemoveChild = (child: Item, index: number) => {
  emit("removeItem", props.item, index);
};
</script>

<template>
  <div class="tree-node">
    <div class="node-row" :class="{ 'is-selected': false }">
      <!-- Icon / Toggler -->
      <span
        class="icon-wrapper"
        @click="toggleOpen"
        :style="{ visibility: item.type === 'folder' ? 'visible' : 'hidden' }"
      >
        <span class="material-icons toggle-icon">
          {{ isOpen ? "expand_more" : "chevron_right" }}
        </span>
      </span>

      <!-- Item Type Icon -->
      <span class="material-icons type-icon" @click="toggleOpen">
        {{
          item.type === "folder"
            ? isOpen
              ? "folder_open"
              : "folder"
            : "description"
        }}
      </span>

      <!-- Name -->
      <span class="name" @click="toggleOpen">{{ item.name || "Root" }}</span>

      <!-- Actions -->
      <div class="actions">
        <!-- Folder Actions -->
        <template v-if="item.type === 'folder'">
          <button
            @click.stop="startAddFile"
            title="Add Request"
            class="icon-btn"
          >
            <span class="material-icons">note_add</span>
          </button>
          <button
            @click.stop="startAddFolder"
            title="Add Group"
            class="icon-btn"
          >
            <span class="material-icons">create_new_folder</span>
          </button>
        </template>
        <!-- Delete is handled by the parent list for children -->
      </div>
    </div>

    <!-- Add Form -->
    <div v-if="isAddingFile || isAddingFolder" class="add-form">
      <input
        v-model="newItemName"
        @keyup.enter="confirmAdd"
        @keyup.esc="cancelAdd"
        placeholder="Name..."
        class="input-name"
        autoFocus
      />
      <button @click="confirmAdd" class="icon-btn action-confirm">
        <span class="material-icons">check</span>
      </button>
      <button @click="cancelAdd" class="icon-btn action-cancel">
        <span class="material-icons">close</span>
      </button>
    </div>

    <!-- Children -->
    <div
      v-if="isOpen && item.children && item.children.length"
      class="children-container"
    >
      <div
        v-for="(child, index) in item.children"
        :key="index"
        class="child-row"
      >
        <div class="child-content">
          <TreeView
            :item="child"
            @add-item="(p, i) => emit('addItem', p, i)"
            @remove-item="(p, idx) => emit('removeItem', p, idx)"
          ></TreeView>
        </div>
        <!-- Delete button for this child -->
        <button
          class="icon-btn delete-btn"
          @click="handleRemoveChild(child, index)"
          title="Delete"
        >
          <span class="material-icons">delete</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tree-node {
  font-family: "Roboto", sans-serif;
  font-size: 14px;
  user-select: none;
  color: #333;
}

.node-row {
  display: flex;
  align-items: center;
  padding: 0 8px;
  height: 32px; /* Dense list item height */
  cursor: pointer;
  border-radius: 4px;
  margin: 1px 4px;
}

.node-row:hover {
  background-color: rgba(0, 0, 0, 0.04);
}

.icon-wrapper {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  margin-right: 4px;
  border-radius: 50%;
}

.icon-wrapper:hover {
  background-color: rgba(0, 0, 0, 0.08);
}

.material-icons {
  font-size: 20px;
  color: #757575;
}

.toggle-icon {
  font-size: 20px;
}

.type-icon {
  margin-right: 8px;
  color: #5f6368; /* Google Grey 700 */
}

/* Specific colors */
.type-icon:contains("folder") {
  /* CSS :contains is not valid, need conditional styling or just rely on class.
       But material folder icons usually grey or specific color.
    */
  color: #5f6368;
}

.name {
  flex-grow: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-weight: 400;
  color: #202124;
}

.actions {
  display: flex;
  align-items: center;
  opacity: 0;
  transition: opacity 0.2s;
}

.node-row:hover .actions {
  opacity: 1;
}

/* Icon Buttons */
.icon-btn {
  background: none;
  border: none;
  cursor: pointer;
  padding: 4px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #5f6368;
  margin-left: 2px;
}

.icon-btn:hover {
  background-color: rgba(0, 0, 0, 0.08); /* Ink ripple effect equivalent */
  color: #202124;
}

.icon-btn .material-icons {
  font-size: 18px;
}

/* Children alignment */
.children-container {
  margin-left: 28px; /* Indent under the type icon */
  /* Remove border line for Material Design usually */
}

.child-row {
  display: flex;
  align-items: flex-start;
  position: relative; /* For delete btn positioning context if needed */
}

.child-content {
  flex-grow: 1;
  min-width: 0;
}

.delete-btn {
  opacity: 0;
  transition: opacity 0.2s;
  margin-top: 0; /* Align with row */
  /* Position it absolutely or just flex? Flex is fine. */
  height: 32px; /* Match row height */
  width: 32px;
}

.child-row:hover > .delete-btn {
  opacity: 1;
}

/* Add Form */
.add-form {
  display: flex;
  align-items: center;
  padding: 0 8px 0 32px;
  height: 32px;
}

.input-name {
  flex-grow: 1;
  border: none;
  border-bottom: 2px solid #6200ee;
  background-color: #f5f5f5;
  font-size: 14px;
  padding: 4px 8px;
  outline: none;
  border-radius: 4px 4px 0 0;
}

.action-confirm .material-icons {
  color: #4caf50;
}
.action-cancel .material-icons {
  color: #f44336;
}
</style>
