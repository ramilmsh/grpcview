<script lang="ts" setup>
import { type Item } from "@/stores/workspace";
import { ref } from "vue";
const props = defineProps<{
  item: Item;
}>();
const emit = defineEmits<{
  addItem: [Item, Item];
}>();
const newItem = ref("");

const addItem = () => {
  emit("addItem", props.item, { name: newItem.value, type: "folder" });
  newItem.value = "";
};
</script>
<template>
  <div>
    <div>{{ item.name }}</div>
    <input v-model="newItem" />
    <div @click="addItem">+</div>
    <ul>
      <li v-for="item in props.item.children">
        <TreeView
          :item="item"
          @add-item="(parent: Item, item: Item) => emit('addItem', parent, item)"
        ></TreeView>
      </li>
    </ul>
  </div>
</template>
